using System.Collections.Concurrent;
using System.IO;
using System.IO.Pipes;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Net192168.Client.Ipc;

/// <summary>
/// Raised when the daemon reports a failure. The message is written for a
/// person, so it can be shown as it is.
/// </summary>
public sealed class DaemonException(string code, string message) : Exception(message)
{
    public string Code { get; } = code;
}

/// <summary>
/// The connection to the daemon.
///
/// Responses and events share one stream and do not take turns, so an event can
/// arrive between a request and its answer. Everything read here is sorted by
/// field: a response carries an id, an event carries a name.
/// </summary>
public sealed class DaemonClient : IAsyncDisposable
{
    private readonly ConcurrentDictionary<string, TaskCompletionSource<JsonNode?>> _pending = new();
    private readonly SemaphoreSlim _writeLock = new(1, 1);

    private NamedPipeClientStream? _pipe;
    private StreamWriter? _writer;
    private CancellationTokenSource? _readLoop;
    private int _nextId;

    /// <summary>Fires for every event the daemon pushes.</summary>
    public event Action<string, JsonNode?>? EventReceived;

    /// <summary>Fires when the connection to the daemon drops.</summary>
    public event Action? Disconnected;

    public bool IsConnected => _pipe?.IsConnected == true;

    /// <summary>
    /// Opens the pipe. The daemon may not be up yet, so a failure here is
    /// ordinary and the caller retries.
    /// </summary>
    public async Task ConnectAsync(CancellationToken cancellationToken)
    {
        var pipe = new NamedPipeClientStream(".", Protocol.PipeName, PipeDirection.InOut, PipeOptions.Asynchronous);
        await pipe.ConnectAsync(2000, cancellationToken);

        _pipe = pipe;
        _writer = new StreamWriter(pipe, new UTF8Encoding(false)) { AutoFlush = false };
        _readLoop = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);

        _ = Task.Run(() => ReadLoopAsync(pipe, _readLoop.Token), CancellationToken.None);
    }

    /// <summary>Calls a method and waits for its result.</summary>
    public async Task<TResult> CallAsync<TResult>(string method, object? parameters, CancellationToken cancellationToken)
    {
        var payload = await InvokeAsync(method, parameters, cancellationToken);
        if (payload is null)
        {
            return default!;
        }
        return payload.Deserialize<TResult>(Protocol.Json)!;
    }

    /// <summary>Calls a method that returns nothing.</summary>
    public Task CallAsync(string method, object? parameters, CancellationToken cancellationToken)
        => InvokeAsync(method, parameters, cancellationToken);

    private async Task<JsonNode?> InvokeAsync(string method, object? parameters, CancellationToken cancellationToken)
    {
        // Every way of reaching the daemon leaves through here, so this is where
        // a missing connection has to become a DaemonException. Callers catch
        // that and nothing else, and the service can be stopped or uninstalled
        // from inside the app, so an unreachable daemon is ordinary rather than
        // exceptional.
        var writer = _writer ?? throw NotConnected();

        var id = $"req_{Interlocked.Increment(ref _nextId)}";
        var completion = new TaskCompletionSource<JsonNode?>(TaskCreationOptions.RunContinuationsAsynchronously);
        _pending[id] = completion;

        var request = new JsonObject
        {
            ["id"] = id,
            ["method"] = method,
        };
        if (parameters is not null)
        {
            request["params"] = JsonSerializer.SerializeToNode(parameters, parameters.GetType(), Protocol.Json);
        }

        try
        {
            await _writeLock.WaitAsync(cancellationToken);
            try
            {
                await writer.WriteLineAsync(request.ToJsonString(Protocol.Json));
                await writer.FlushAsync(cancellationToken);
            }
            catch (Exception error) when (error is ObjectDisposedException or IOException)
            {
                // The pipe went away between the check above and the write,
                // which is what happens when the service stops mid-click.
                throw NotConnected();
            }
            finally
            {
                _writeLock.Release();
            }

            await using (cancellationToken.Register(() => completion.TrySetCanceled(cancellationToken)))
            {
                return await completion.Task;
            }
        }
        finally
        {
            _pending.TryRemove(id, out _);
        }
    }

    private static DaemonException NotConnected() =>
        new("disconnected", "Lost the connection to the background service.");

    private async Task ReadLoopAsync(NamedPipeClientStream pipe, CancellationToken cancellationToken)
    {
        try
        {
            using var reader = new StreamReader(pipe, new UTF8Encoding(false));
            while (!cancellationToken.IsCancellationRequested)
            {
                var line = await reader.ReadLineAsync(cancellationToken);
                if (line is null)
                {
                    break;
                }
                if (line.Length == 0)
                {
                    continue;
                }
                Dispatch(line);
            }
        }
        catch (Exception) when (!cancellationToken.IsCancellationRequested)
        {
            // The daemon went away. Callers waiting on a reply are released
            // below and the app reconnects.
        }
        finally
        {
            FailPending();
            Disconnected?.Invoke();
        }
    }

    private void Dispatch(string line)
    {
        JsonNode? message;
        try
        {
            message = JsonNode.Parse(line);
        }
        catch (JsonException)
        {
            return;
        }
        if (message is not JsonObject payload)
        {
            return;
        }

        if (payload["event"] is JsonNode name)
        {
            EventReceived?.Invoke(name.GetValue<string>(), payload["data"]);
            return;
        }

        if (payload["id"]?.GetValue<string>() is not string id || !_pending.TryRemove(id, out var completion))
        {
            return;
        }

        if (payload["ok"]?.GetValue<bool>() == true)
        {
            completion.TrySetResult(payload["result"]);
            return;
        }

        var error = payload["error"];
        completion.TrySetException(new DaemonException(
            error?["code"]?.GetValue<string>() ?? "internal",
            error?["message"]?.GetValue<string>() ?? "Something went wrong."));
    }

    private void FailPending()
    {
        foreach (var (id, completion) in _pending)
        {
            _pending.TryRemove(id, out _);
            completion.TrySetException(new DaemonException("disconnected", "Lost the connection to the background service."));
        }
    }

    public async ValueTask DisposeAsync()
    {
        _readLoop?.Cancel();
        FailPending();

        if (_writer is not null)
        {
            await _writer.DisposeAsync();
        }
        if (_pipe is not null)
        {
            await _pipe.DisposeAsync();
        }
        _readLoop?.Dispose();
        _writeLock.Dispose();
    }
}
