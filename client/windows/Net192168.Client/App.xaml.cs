using System.IO;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Net192168.Client.Ipc;
using Net192168.Client.Services;

namespace Net192168.Client;

public partial class App : Application
{
    private Window? _window;

    public App()
    {
        InitializeComponent();

        // An unhandled exception closes the window with no message and no log,
        // which is indistinguishable from the user clicking the X. A missing
        // resource in a dialog did exactly that once already, so anything that
        // gets this far leaves a file behind.
        UnhandledException += (_, e) =>
        {
            Log(e.Exception);
            e.Handled = false;
        };
        AppDomain.CurrentDomain.UnhandledException += (_, e) => Log(e.ExceptionObject as Exception);
        TaskScheduler.UnobservedTaskException += (_, e) => Log(e.Exception);
    }

    /// <summary>
    /// The connection to the daemon, shared by every page.
    ///
    /// The daemon is a separate process and the source of truth for everything
    /// the app shows, so there is one of these and it lives as long as the app.
    /// </summary>
    public static Daemon Daemon { get; private set; } = null!;

    /// <summary>Where this app's log is written, beside the daemon's.</summary>
    public static string LogPath { get; } = ResolveLogPath();

    /// <summary>The folder holding both logs, which is what About opens.</summary>
    public static string LogFolder { get; } = Path.GetDirectoryName(LogPath)!;

    /// <summary>
    /// Puts this log next to the daemon's, so there is one place to look and
    /// one folder to ask somebody for.
    ///
    /// The daemon runs as a service and writes to ProgramData, which it opens
    /// to local users for exactly this. Falls back to this user's own folder
    /// when that is not writable, which means a development build where no
    /// service ever created it.
    /// </summary>
    private static string ResolveLogPath()
    {
        var shared = Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData),
            "192168",
            "logs");

        try
        {
            Directory.CreateDirectory(shared);
            var path = Path.Combine(shared, "client.log");
            // Opening it is the only honest test of whether it can be written.
            using (new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.ReadWrite))
            {
            }
            return path;
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            return Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
                "192168",
                "client.log");
        }
    }

    /// <summary>
    /// Whether Windows started this at sign-in, in which case the app stays in
    /// the tray.
    /// </summary>
    public static bool StartedHidden { get; private set; }

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        StartedHidden = Environment.GetCommandLineArgs()
            .Contains(StartWithWindows.TrayArgument, StringComparer.OrdinalIgnoreCase);

        Daemon = new Daemon(DispatcherQueue.GetForCurrentThread());
        _ = Daemon.RunAsync();

        // The service starts on demand, and opening the app is that demand. Does
        // nothing if it was never installed, which is the development case.
        _ = EnsureServiceRunningAsync();

        // Once, on the way in. It only looks: nothing is downloaded and nothing
        // is replaced.
        _ = Updates.CheckAsync();

        _window = new MainWindow();
        _window.Closed += (_, _) => Daemon.Shutdown();

        // Started at sign-in, the tray icon is the whole UI until asked for more.
        if (!StartedHidden)
        {
            _window.Activate();
        }
    }

    private static async Task EnsureServiceRunningAsync()
    {
        if (!DaemonService.IsAvailable)
        {
            return;
        }
        if (await DaemonService.QueryAsync() == ServiceState.Stopped)
        {
            await DaemonService.StartAsync();
        }
    }

    /// <summary>Writes a diagnostic line to the same file as crashes.</summary>
    public static void Trace(string message)
    {
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(LogPath)!);
            File.AppendAllText(LogPath, $"{DateTimeOffset.Now:O} {message}{Environment.NewLine}{Environment.NewLine}");
        }
        catch (IOException)
        {
        }
    }

    private static void Log(Exception? error)
    {
        if (error is null)
        {
            return;
        }
        try
        {
            Directory.CreateDirectory(Path.GetDirectoryName(LogPath)!);
            File.AppendAllText(LogPath, $"{DateTimeOffset.Now:O}\n{error}\n\n");
        }
        catch (IOException)
        {
            // Losing the log is not worth a second crash on the way out.
        }
    }
}
