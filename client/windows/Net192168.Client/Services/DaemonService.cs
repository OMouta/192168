using System.Diagnostics;

namespace Net192168.Client.Services;

/// <summary>What the background service is doing.</summary>
public enum ServiceState
{
    /// <summary>Not registered with Windows. Either not installed, or removed on purpose.</summary>
    Absent,

    /// <summary>Registered and not running.</summary>
    Stopped,

    /// <summary>Up, and serving the pipe.</summary>
    Running,

    /// <summary>Windows is starting or stopping it.</summary>
    Pending,

    /// <summary>The daemon could not be asked, so we genuinely do not know.</summary>
    Unknown,
}

/// <summary>
/// Controls the Windows service the daemon runs as.
///
/// Shells out to the daemon rather than calling the Service Control Manager, so
/// the access rights live in one place.
///
/// Start and stop need no elevation. Install and remove do.
/// </summary>
public static class DaemonService
{
    /// <summary>How long a service command may take. Stopping waits for the
    /// adapter to come down.</summary>
    private static readonly TimeSpan Timeout = TimeSpan.FromSeconds(45);

    /// <summary>
    /// Where the daemon lives. Installed, it sits beside the client. The
    /// environment variable points at a development build instead.
    /// </summary>
    public static string ExecutablePath
    {
        get
        {
            var overridden = Environment.GetEnvironmentVariable("NET192168_DAEMON_PATH");
            if (!string.IsNullOrWhiteSpace(overridden))
            {
                return overridden;
            }

            var here = Path.GetDirectoryName(Environment.ProcessPath) ?? AppContext.BaseDirectory;
            return Path.Combine(here, "192168-service.exe");
        }
    }

    /// <summary>Whether the daemon executable is where we expect it.</summary>
    public static bool IsAvailable => File.Exists(ExecutablePath);

    /// <summary>Asks Windows what the service is doing.</summary>
    public static async Task<ServiceState> QueryAsync()
    {
        var (exitCode, output) = await RunAsync("status", elevated: false);
        if (exitCode != 0)
        {
            return ServiceState.Unknown;
        }

        return output.Trim() switch
        {
            "absent" => ServiceState.Absent,
            "stopped" => ServiceState.Stopped,
            "running" => ServiceState.Running,
            "pending" => ServiceState.Pending,
            _ => ServiceState.Unknown,
        };
    }

    /// <summary>Starts the service. No elevation needed.</summary>
    public static async Task<bool> StartAsync() => (await RunAsync("start", elevated: false)).ExitCode == 0;

    /// <summary>Stops the service, which takes any tunnel down with it.</summary>
    public static async Task<bool> StopAsync() => (await RunAsync("stop", elevated: false)).ExitCode == 0;

    /// <summary>Registers the service. Prompts for administrator rights.</summary>
    public static async Task<bool> InstallAsync() => (await RunAsync("install", elevated: true)).ExitCode == 0;

    /// <summary>
    /// Stops the service, removes the network adapter, and deregisters it.
    /// Prompts for administrator rights.
    /// </summary>
    public static async Task<bool> RemoveAsync() => (await RunAsync("uninstall", elevated: true)).ExitCode == 0;

    /// <summary>
    /// Opens the Windows list of installed apps. Not a silent uninstall from in
    /// here, which would mean removing the app while it is running.
    /// </summary>
    public static void OpenWindowsUninstaller()
    {
        Process.Start(new ProcessStartInfo("ms-settings:appsfeatures") { UseShellExecute = true });
    }

    private static async Task<(int ExitCode, string Output)> RunAsync(string verb, bool elevated)
    {
        if (!IsAvailable)
        {
            return (-1, "");
        }

        var info = new ProcessStartInfo(ExecutablePath)
        {
            // Elevated processes cannot have their output redirected, and a
            // console would flash up on every status check.
            UseShellExecute = elevated,
            CreateNoWindow = !elevated,
            RedirectStandardOutput = !elevated,
            RedirectStandardError = !elevated,
        };
        info.ArgumentList.Add("service");
        info.ArgumentList.Add(verb);
        if (elevated)
        {
            info.Verb = "runas";
        }

        try
        {
            using var process = Process.Start(info);
            if (process is null)
            {
                return (-1, "");
            }

            // Both pipes are drained. Redirecting stderr and never reading it
            // deadlocks the child once its buffer fills, and a stop that never
            // returns is indistinguishable from one that never ran.
            var output = "";
            if (!elevated)
            {
                var stdout = process.StandardOutput.ReadToEndAsync();
                var stderr = process.StandardError.ReadToEndAsync();
                output = await stdout;
                await stderr;
            }

            using var deadline = new CancellationTokenSource(Timeout);
            await process.WaitForExitAsync(deadline.Token);
            return (process.ExitCode, output);
        }
        catch (OperationCanceledException)
        {
            // Still running somewhere, so we do not know that it worked.
            return (-1, "");
        }
        catch (Exception)
        {
            // Usually the user dismissed the UAC prompt.
            return (-1, "");
        }
    }
}
