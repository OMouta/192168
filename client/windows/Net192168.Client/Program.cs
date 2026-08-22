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
            // Kept alive, because the subscription below is on it and a local
            // would let both be collected.
            _instance = main;

            // Every later launch arrives here, since a running instance is
            // never launched again.
            main.Activated += (_, e) => (Application.Current as App)?.OnRedirected(e);
            return false;
        }

        // Off this thread, and not for long. This STA is not pumping messages
        // yet and the redirect answers through COM, which needs one, so waiting
        // on it here can hang. A launch that hangs leaves a process with no
        // window to close it by.
        var activation = AppInstance.GetCurrent().GetActivatedEventArgs();
        Task.Run(() => main.RedirectActivationToAsync(activation).AsTask())
            .Wait(TimeSpan.FromSeconds(5));
        return true;
    }

    private static AppInstance? _instance;
}
