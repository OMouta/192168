using CommunityToolkit.Mvvm.Input;
using Microsoft.UI;
using Microsoft.UI.Input;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;
using Net192168.Client.Ipc;
using Net192168.Client.Services;
using Net192168.Client.Views;
using Windows.Foundation;
using Windows.Graphics;

namespace Net192168.Client;

public sealed partial class MainWindow : Window
{
    // A group is under ten people and every row is one line, so the window only
    // has to be tall enough for that. Anything wider is empty space beside a
    // game.
    private const int Width = 400;
    private const int Height = 560;

    // What the window buttons take up on the right at 100% scale, used only if
    // the window has not reported its own inset yet. Putting the gear under the
    // close button is worse than leaving a gap.
    private const double CaptionButtonsFallback = 144;

    public MainWindow()
    {
        InitializeComponent();

        // The system title bar follows the system theme rather than the app's,
        // so it comes out light on most machines. Extending into it keeps the
        // top of the window dark and puts the wordmark level with the window
        // buttons.
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(TitleBarRow);

        // The window buttons are drawn by Windows from the top of the window
        // down, so the header only looks level with them when the row is
        // exactly as tall as they are. Tall is 48, which TitleBarRow matches.
        // Left at Standard the buttons are 32 and sit six pixels high of the
        // wordmark and the gear.
        AppWindow.TitleBar.PreferredHeightOption = TitleBarHeightOption.Tall;

        AppWindow.Title = "192168";
        AppWindow.SetIcon("Assets/icon.ico");
        AppWindow.Resize(new SizeInt32(Width, Height));

        AppWindow.TitleBar.ButtonBackgroundColor = Colors.Transparent;
        AppWindow.TitleBar.ButtonInactiveBackgroundColor = Colors.Transparent;

        if (AppWindow.Presenter is OverlappedPresenter presenter)
        {
            // Maximising a window this dense would only stretch the gaps.
            presenter.IsMaximizable = false;
            presenter.PreferredMinimumWidth = Width;
            presenter.PreferredMinimumHeight = 420;
        }

        TitleBarRow.Loaded += (_, _) => LayOutTitleBar();
        TitleBarRow.SizeChanged += (_, _) => LayOutTitleBar();

        ContentFrame.Navigated += OnNavigated;

        // Closing hides rather than quits. The daemon holds the tunnels open on
        // its own, so ending the app on a close would leave a live connection
        // with nothing showing it.
        AppWindow.Closing += (_, args) =>
        {
            if (_exiting)
            {
                return;
            }
            args.Cancel = true;
            AppWindow.Hide();
        };

        App.Daemon.StateChanged += UpdateTray;
        UpdateTray();

        ContentFrame.Navigate(typeof(HomePage));
    }

    /// <summary>True once Exit was chosen, so the close is allowed through.</summary>
    private bool _exiting;

    /// <summary>
    /// What the tray says without being opened: which group, and how many
    /// people are on it. This is the whole reason to leave the app running
    /// with no window.
    /// </summary>
    private void UpdateTray()
    {
        var state = App.Daemon.State;
        var connected = state.Connection == ConnectionState.Connected;

        TrayDisconnect.IsEnabled = connected;
        Tray.ToolTipText = connected
            ? $"192168 - {state.GroupName}, {Describe(state.Peers.Count)}"
            : "192168 - not connected";
    }

    private static string Describe(int peers) => peers switch
    {
        0 => "nobody else online",
        1 => "1 other online",
        _ => $"{peers} others online",
    };

    /// <summary>
    /// Brings the window back from the tray. Showing is not enough on its own:
    /// a window restored behind whatever is in front of it reads as a click
    /// that did nothing.
    /// </summary>
    [RelayCommand]
    private void ShowWindow()
    {
        AppWindow.Show();
        Activate();
    }

    [RelayCommand]
    private async Task TrayDisconnectAsync()
    {
        try
        {
            await App.Daemon.DisconnectAsync();
        }
        catch (DaemonException error)
        {
            // Nothing is on screen to show this on, and the state the tray
            // reports is about to say whether it worked anyway.
            App.Trace($"tray disconnect failed: {error.Code} {error.Message}");
        }
    }

    /// <summary>
    /// Quits, and takes the background service with it. Leaving it up would
    /// leave an adapter and live tunnels with no icon left to stop them from.
    ///
    /// A command rather than a Click handler. The tray's flyout lives in its own
    /// window, and a Click on an item in there never reached this code, so the
    /// menu did nothing at all. The left-click binding always worked, which is
    /// what says commands are the way across that boundary.
    /// </summary>
    [RelayCommand]
    private async Task ExitAppAsync()
    {
        _exiting = true;
        App.Daemon.StateChanged -= UpdateTray;

        // Quitting must not depend on the service agreeing to stop, or on the
        // stop returning at all.
        try
        {
            if (DaemonService.IsAvailable)
            {
                await Task.WhenAny(DaemonService.StopAsync(), Task.Delay(TimeSpan.FromSeconds(15)));
            }
        }
        catch (Exception error)
        {
            App.Trace($"tray exit: stopping the service failed: {error}");
        }

        // The icon outlives the process unless it is taken down by hand, which
        // leaves a dead entry in the tray until something hovers over it.
        Tray.Dispose();
        App.Daemon.Shutdown();

        // Application.Exit closes windows, and this window is hidden rather
        // than open, so there is nothing for it to close and the process stays
        // up serving the tray icon's message loop. Everything worth shutting
        // down has been by now, and a tray app that will not quit is worse than
        // one that quits abruptly.
        Environment.Exit(0);
    }

    /// <summary>
    /// Puts the header in the state the screen that just arrived needs: its
    /// name instead of the wordmark, back once there is somewhere to go back
    /// to, and the gear only on home, where it is the way forward.
    /// </summary>
    private void OnNavigated(object sender, NavigationEventArgs e)
    {
        var title = e.Content switch
        {
            SettingsPage => "Settings",
            AboutPage => "About",
            ManageGroupPage => "Group",
            GroupPage page => page.ViewModel.Title,
            _ => null,
        };

        PageTitle.Text = title ?? "";
        PageTitle.Visibility = Show(title is not null);
        Wordmark.Visibility = Show(title is null);
        SettingsButton.Visibility = Show(title is null);
        BackButton.Visibility = Show(ContentFrame.CanGoBack);

        LayOutTitleBar();
    }

    private static Visibility Show(bool visible) => visible ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>
    /// Keeps the gear clear of the window buttons, and marks both header
    /// buttons as places the window should not treat as a title bar.
    ///
    /// Everything inside the element given to SetTitleBar is a drag handle, and
    /// a button in there receives input it should not: the first version of this
    /// opened the settings dialog by itself when the window was activated.
    /// </summary>
    private void LayOutTitleBar()
    {
        if (Content?.XamlRoot is null)
        {
            return;
        }

        var scale = Content.XamlRoot.RasterizationScale;
        var inset = AppWindow.TitleBar.RightInset / scale;
        var margin = new Thickness(0, 0, (inset > 0 ? inset : CaptionButtonsFallback) + 4, 0);
        if (SettingsButton.Margin != margin)
        {
            SettingsButton.Margin = margin;
        }

        // The rectangles below are read off the laid-out positions, so the
        // margin above has to have taken effect first.
        TitleBarRow.UpdateLayout();

        List<RectInt32> passthrough = [];
        Cut(BackButton);
        Cut(SettingsButton);

        InputNonClientPointerSource
            .GetForWindowId(AppWindow.Id)
            .SetRegionRects(NonClientRegionKind.Passthrough, [.. passthrough]);

        void Cut(FrameworkElement element)
        {
            if (element.Visibility != Visibility.Visible || element.ActualWidth == 0)
            {
                return;
            }

            var bounds = element
                .TransformToVisual(Content)
                .TransformBounds(new Rect(0, 0, element.ActualWidth, element.ActualHeight));

            passthrough.Add(new RectInt32(
                (int)(bounds.X * scale),
                (int)(bounds.Y * scale),
                (int)(bounds.Width * scale),
                (int)(bounds.Height * scale)));
        }
    }

    private void OnBack(object sender, RoutedEventArgs e)
    {
        if (ContentFrame.CanGoBack)
        {
            ContentFrame.GoBack();
        }
    }

    private void OnSettings(object sender, RoutedEventArgs e)
    {
        // Navigating to the page already on screen would push a second copy
        // onto the back stack, so a double click has to be harmless.
        if (ContentFrame.CurrentSourcePageType != typeof(SettingsPage))
        {
            ContentFrame.Navigate(typeof(SettingsPage));
        }
    }
}
