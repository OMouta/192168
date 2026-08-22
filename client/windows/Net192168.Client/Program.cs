using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.Windows.AppLifecycle;

namespace Net192168.Client;

/// <summary>
/// The entry point, written by hand rather than generated, because there is one
/// thing to do before XAML starts.
///
/// A link opens the app by running the executable again. Without this, clicking
/// one while the app is already open would start a second copy: two tray icons,
/// two connections to the daemon, and a second window nobody asked for. This
/// hands the link to the copy already running and quits.
/// </summary>
public static class Program
{
    [STAThread]
    private static void Main(string[] args)
    {
        if (HandOverToRunningInstance())
        {
            return;
        }

        WinRT.ComWrappersSupport.InitializeComWrappers();
        Application.Start(_ =>
        {
            var context = new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread());
            SynchronizationContext.SetSynchronizationContext(context);
            // Constructed for its side effects: the application takes itself
            // from here, and OnLaunched is what runs next.
            new App();
        });
    }

    /// <summary>
    /// Gives this launch to the instance already running, if there is one.
    /// </summary>
    /// <returns>Whether this process is finished and should exit.</returns>
    private static bool HandOverToRunningInstance()
    {
        // The key names the single instance rather than this process. Whoever
        // registers it first is the app; everybody after that finds it.
        var main = AppInstance.FindOrRegisterForKey("192168");
        if (main.IsCurrent)
        {
            // Links that arrive later come in through here, since a running
            // instance is never launched again.
            main.Activated += (_, e) => (Application.Current as App)?.OnRedirected(e);
            return false;
        }

        // Blocking is what the wait is for: nothing has started yet, and there
        // is nothing else for this process to do before it goes.
        main.RedirectActivationToAsync(AppInstance.GetCurrent().GetActivatedEventArgs()).AsTask().GetAwaiter().GetResult();
        return true;
    }
}
