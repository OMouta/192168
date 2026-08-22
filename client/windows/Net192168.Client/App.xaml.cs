using System.IO;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.Windows.AppLifecycle;
using Net192168.Client.Ipc;
using Net192168.Client.Services;

namespace Net192168.Client;

public partial class App : Application
{
    private MainWindow? _window;

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

        // An invite link opens the app on the group it names.
        var invite = InviteFrom(AppInstance.GetCurrent().GetActivatedEventArgs()) ?? InviteFromCommandLine();

        // Started at sign-in, the tray icon is the whole UI until asked for more.
        if (!StartedHidden)
        {
            _window.Activate();
        }

        // A link opens the window even when this launch was meant to stay in
        // the tray.
        if (invite is not null)
        {
            _window.OpenInvite(invite);
        }
    }

    /// <summary>
    /// A launch handed over by a second copy of the app, on that copy's thread.
    ///
    /// Running the app again is how people ask for the window back, so every
    /// one of these opens it. Only acting on the ones carrying a link left the
    /// app in the tray with the shortcut doing nothing.
    /// </summary>
    internal void OnRedirected(AppActivationArguments args)
    {
        var invite = InviteFrom(args);

        _window?.DispatcherQueue.TryEnqueue(() =>
        {
            if (invite is not null)
            {
                _window?.OpenInvite(invite);
                return;
            }

            _window?.ShowWindow();
        });
    }

    /// <summary>The invite an activation carries, or null. Whether the string
    /// is a real invite is the daemon's problem.</summary>
    private static string? InviteFrom(AppActivationArguments? args)
    {
        // Fully qualified. Importing this namespace would shadow the
        // LaunchActivatedEventArgs OnLaunched is handed.
        return args?.Data is Windows.ApplicationModel.Activation.IProtocolActivatedEventArgs protocol
            ? protocol.Uri.AbsoluteUri
            : null;
    }

    /// <summary>
    /// The invite this process was started with, since unpackaged a protocol
    /// launch can arrive as a plain argument. This launch only: on a redirect
    /// the arguments are still ours, so reading them there would reopen the
    /// link that started this instance every time somebody ran the app again.
    /// </summary>
    private static string? InviteFromCommandLine()
        => Environment.GetCommandLineArgs()
            .Skip(1)
            .FirstOrDefault(argument => argument.StartsWith(InviteScheme, StringComparison.OrdinalIgnoreCase));

    /// <summary>The URI scheme setup registers this app for. It starts with a
    /// letter because a scheme has to.</summary>
    private const string InviteScheme = "net192168:";

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
