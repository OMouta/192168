using System.Text.Json;
using System.Text.Json.Nodes;
using Microsoft.UI.Dispatching;

namespace Net192168.Client.Ipc;

/// <summary>
/// The app's connection to the daemon.
///
/// The daemon decides what the state is. This holds the latest copy, keeps the
/// connection up, and raises changes on the UI thread so views can bind to it
/// without thinking about threads.
/// </summary>
public sealed class Daemon
{
    private readonly DispatcherQueue _dispatcher;
    private readonly CancellationTokenSource _lifetime = new();
    private DaemonClient _client = new();

    public Daemon(DispatcherQueue dispatcher)
    {
        _dispatcher = dispatcher;
    }

    /// <summary>The last state the daemon reported.</summary>
    public DaemonState State { get; private set; } = new();

    /// <summary>Whether the app is talking to the daemon at all.</summary>
    public bool IsAvailable { get; private set; }

    public event Action? StateChanged;

    /// <summary>
    /// Keeps the connection up for the life of the app. The daemon is a separate
    /// process that can be restarted or not running yet, so this retries rather
    /// than failing once and giving up.
    /// </summary>
    public async Task RunAsync()
    {
        while (!_lifetime.IsCancellationRequested)
        {
            try
            {
                await ConnectOnceAsync();
            }
            catch (OperationCanceledException)
            {
                return;
            }
            catch (Exception)
            {
                // The daemon is not up. Nothing to report beyond the state
                // below, which already says the app cannot reach it.
            }

            SetAvailable(false);
            try
            {
                await Task.Delay(TimeSpan.FromSeconds(2), _lifetime.Token);
            }
            catch (OperationCanceledException)
            {
                return;
            }
        }
    }

    private async Task ConnectOnceAsync()
    {
        var client = new DaemonClient();
        var closed = new TaskCompletionSource();

        client.EventReceived += OnEvent;
        client.Disconnected += () => closed.TrySetResult();

        await client.ConnectAsync(_lifetime.Token);
        _client = client;

        // The first thing to do is ask what the world looks like, because
        // events only describe changes from here on.
        var state = await client.CallAsync<DaemonState>("GetState", null, _lifetime.Token);
        Post(() =>
        {
            State = state;
            IsAvailable = true;
            StateChanged?.Invoke();
        });

        await closed.Task;
        await client.DisposeAsync();
    }

    private void OnEvent(string name, JsonNode? data)
    {
        // StateChanged carries a whole snapshot, so a client that missed an
        // event is put back in line by the next one.
        if (name == "StateChanged" && data is not null)
        {
            var state = data.Deserialize<DaemonState>(Protocol.Json);
            if (state is not null)
            {
                Post(() =>
                {
                    State = state;
                    StateChanged?.Invoke();
                });
            }
            return;
        }

        // Everything else is a hint that something moved. Asking for the whole
        // state is cheap over a local pipe and avoids keeping two copies of the
        // truth in step.
        if (name is "GroupConnected" or "GroupDisconnected" or "PeerAdded" or "PeerRemoved"
            or "PeerStateChanged" or "PeerLatencyChanged" or "ServerConnectionChanged")
        {
            _ = RefreshAsync();
        }
    }

    /// <summary>Asks the daemon for the current state.</summary>
    public async Task RefreshAsync()
    {
        try
        {
            var state = await _client.CallAsync<DaemonState>("GetState", null, _lifetime.Token);
            Post(() =>
            {
                State = state;
                StateChanged?.Invoke();
            });
        }
        catch (Exception)
        {
            // A refresh that fails means the connection is going away, and the
            // reconnect loop already handles that.
        }
    }

    public async Task<Group[]> GetGroupsAsync()
    {
        var result = await _client.CallAsync<GetGroupsResult>("GetGroups", null, _lifetime.Token);
        return result.Groups.ToArray();
    }

    public async Task<Group> CreateGroupAsync(string name, string icon, string color)
    {
        var result = await _client.CallAsync<GroupResult>(
            "CreateGroup", new CreateGroupParams(name, icon, color), _lifetime.Token);
        return result.Group;
    }

    /// <summary>Joins whichever group an invite opens. The code may be a link.</summary>
    public async Task<Group> JoinGroupAsync(string code)
    {
        var result = await _client.CallAsync<GroupResult>(
            "JoinGroup", new JoinGroupParams(code), _lifetime.Token);
        return result.Group;
    }

    /// <summary>What an invite opens, without taking it.</summary>
    public Task<InviteResult> GetInviteAsync(string code)
        => _client.CallAsync<InviteResult>("GetInvite", new InviteParams(code), _lifetime.Token);

    /// <summary>Replaces a group's code. The old one stops working.</summary>
    public Task<InviteCodeResult> ResetInviteAsync(string groupId)
        => _client.CallAsync<InviteCodeResult>("ResetInvite", new GroupParams(groupId), _lifetime.Token);

    public Task ConnectGroupAsync(string groupId)
        => _client.CallAsync("ConnectGroup", new GroupParams(groupId), _lifetime.Token);

    public Task DisconnectAsync()
        => _client.CallAsync("Disconnect", null, _lifetime.Token);

    public Task LeaveGroupAsync(string groupId)
        => _client.CallAsync("LeaveGroup", new GroupParams(groupId), _lifetime.Token);

    public Task SetNicknameAsync(string nickname)
        => _client.CallAsync("SetNickname", new SetNicknameParams(nickname), _lifetime.Token);

    public Task<GetServerResult> GetServerAsync()
        => _client.CallAsync<GetServerResult>("GetServer", null, _lifetime.Token);

    public Task SetServerAsync(string url)
        => _client.CallAsync("SetServer", new ServerParams(url), _lifetime.Token);

    public Task<LanDiscoveryResult> GetLanDiscoveryAsync()
        => _client.CallAsync<LanDiscoveryResult>("GetLanDiscovery", null, _lifetime.Token);

    public Task<LanDiscoveryResult> SetLanDiscoveryAsync(bool enabled)
        => _client.CallAsync<LanDiscoveryResult>("SetLanDiscovery", new LanDiscoveryParams(enabled), _lifetime.Token);

    public Task<PacketLogResult> GetPacketLogAsync()
        => _client.CallAsync<PacketLogResult>("GetPacketLog", null, _lifetime.Token);

    public Task<PacketLogResult> SetPacketLogAsync(bool enabled)
        => _client.CallAsync<PacketLogResult>("SetPacketLog", new PacketLogParams(enabled), _lifetime.Token);

    public Task<ClearLogsResult> ClearLogsAsync()
        => _client.CallAsync<ClearLogsResult>("ClearLogs", null, _lifetime.Token);

    public Task<TestServerResult> TestServerAsync(string url)
        => _client.CallAsync<TestServerResult>("TestServer", new ServerParams(url), _lifetime.Token);

    public Task RetryPeerAsync(string deviceId)
        => _client.CallAsync("RetryPeer", new PeerParams(deviceId), _lifetime.Token);

    public Task RemoveMemberAsync(string groupId, string deviceId)
        => _client.CallAsync("RemoveMember", new MemberParams(groupId, deviceId), _lifetime.Token);

    public Task RenameGroupAsync(string groupId, string name)
        => _client.CallAsync("RenameGroup", new RenameGroupParams(groupId, name), _lifetime.Token);

    public Task SetGroupAppearanceAsync(string groupId, string icon, string color)
        => _client.CallAsync("SetGroupAppearance", new SetGroupAppearanceParams(groupId, icon, color), _lifetime.Token);

    public Task TransferOwnershipAsync(string groupId, string deviceId)
        => _client.CallAsync("TransferOwnership", new MemberParams(groupId, deviceId), _lifetime.Token);

    public Task DeleteGroupAsync(string groupId)
        => _client.CallAsync("DeleteGroup", new GroupParams(groupId), _lifetime.Token);

    public Task<GetServerResult> ResetSettingsAsync()
        => _client.CallAsync<GetServerResult>("ResetSettings", null, _lifetime.Token);

    public void Shutdown() => _lifetime.Cancel();

    private void SetAvailable(bool available) => Post(() =>
    {
        if (IsAvailable == available)
        {
            return;
        }
        IsAvailable = available;
        StateChanged?.Invoke();
    });

    private void Post(Action action)
    {
        if (_dispatcher.HasThreadAccess)
        {
            action();
            return;
        }
        _dispatcher.TryEnqueue(() => action());
    }
}
