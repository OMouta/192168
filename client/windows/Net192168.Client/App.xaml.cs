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
    }

    /// <summary>
    /// The connection to the daemon, shared by every page.
    ///
    /// The daemon is a separate process and the source of truth for everything
    /// the app shows, so there is one of these and it lives as long as the app.
    /// </summary>
    public static Daemon Daemon { get; private set; } = null!;

    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        Daemon = new Daemon(DispatcherQueue.GetForCurrentThread());
        _ = Daemon.RunAsync();

        _window = new MainWindow();
        _window.Closed += (_, _) => Daemon.Shutdown();
        _window.Activate();
    }
}
