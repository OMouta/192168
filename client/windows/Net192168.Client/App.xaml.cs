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

    /// <summary>Where a crash is written, next to the daemon's own state.</summary>
    public static string LogPath { get; } = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.ApplicationData),
        "192168",
        "client-crash.log");

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
