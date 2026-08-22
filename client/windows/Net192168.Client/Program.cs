using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.Windows.AppLifecycle;

namespace Net192168.Client;

/// <summary>
/// The entry point, hand-written because something has to happen before XAML
/// starts.
///
/// A link opens the app by running the exe again. Without this, clicking one
/// while the app is open would give you two tray icons and two connections to
/// the daemon. This hands the link over and quits.
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
            // Constructed for its side effects. OnLaunched runs next.
            new App();
        });
    }

    /// <summary>
    /// Gives this launch to the instance already running, if there is one.
    /// </summary>
    /// <returns>Whether this process is finished and should exit.</returns>
    private static bool HandOverToRunningInstance()
    {
        // The key names the single instance, not this process. Whoever
        // registers first is the app.
        var main = AppInstance.FindOrRegisterForKey("192168");
        if (main.IsCurrent)
        {
            // Later links arrive here, since a running instance is never
            // launched again.
            main.Activated += (_, e) => (Application.Current as App)?.OnRedirected(e);
            return false;
        }

        // Blocking is fine. Nothing has started, and this process is leaving.
        main.RedirectActivationToAsync(AppInstance.GetCurrent().GetActivatedEventArgs()).AsTask().GetAwaiter().GetResult();
        return true;
    }
}
