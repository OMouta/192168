using System.IO;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Net192168.Client.Ipc;

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

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        Daemon = new Daemon(DispatcherQueue.GetForCurrentThread());
        _ = Daemon.RunAsync();

        _window = new MainWindow();
        _window.Closed += (_, _) => Daemon.Shutdown();
        _window.Activate();
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
