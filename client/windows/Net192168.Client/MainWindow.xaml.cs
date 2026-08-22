using CommunityToolkit.Mvvm.Input;
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

    public MainWindow()
    {
        InitializeComponent();

        AppWindow.Title = "192168";
        AppWindow.SetIcon("Assets/icon.ico");
        AppWindow.Resize(new SizeInt32(Width, Height));

        if (AppWindow.Presenter is OverlappedPresenter presenter)
        {
            // Windows draws the three window buttons as a set, and a window
            // that cannot be maximised keeps a greyed one taking up space to
            // say no. No system title bar at all, then, and the two buttons
            // worth having are drawn in XAML.
            presenter.SetBorderAndTitleBar(hasBorder: true, hasTitleBar: false);
            presenter.IsMaximizable = false;
            presenter.PreferredMinimumWidth = Width;
            presenter.PreferredMinimumHeight = 420;
        }

        TitleBarRow.Loaded += (_, _) => LayOutDragRegions();
        TitleBarRow.SizeChanged += (_, _) => LayOutDragRegions();

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

        // Who is online is why somebody opens this window, and the list is only
        // fetched when the page is navigated to. Coming back from the tray is
        // not a navigation, so the counts would be as old as the last visit.
        //
        // Only while disconnected, which is the only time that list is on
        // screen. Alt-tabbing during a game asks the server for nothing.
        Activated += (_, args) =>
        {
            if (args.WindowActivationState != WindowActivationState.Deactivated
                && App.Daemon.State.Connection != ConnectionState.Connected
                && ContentFrame.Content is HomePage home)
            {
                _ = home.RefreshAsync();
            }
        };

        App.Daemon.StateChanged += UpdateTray;
        UpdateTray();

        App.Daemon.StateChanged += AnnounceArrivals;
        AnnounceArrivals();

        // The installer kills whatever still holds the files it replaces, and a
        // killed process leaves its tray icon behind. Quit first.
        Updates.Installing += Quit;

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

    /// <summary>Who was on the network last time the state changed. Arriving is
    /// the event, so the difference is what matters and not the list.</summary>
    private readonly HashSet<string> _here = [];

    /// <summary>The group the set above belongs to, so joining a different one
    /// starts over rather than announcing everybody in it.</summary>
    private string _announcingFor = "";

    /// <summary>
    /// Says when somebody joins the network. The window is behind a game when
    /// that happens, which is the whole point of it.
    ///
    /// Nobody is announced on the way in. Everyone already there would arrive at
    /// once, which is a stack of notifications saying what the screen behind
    /// them already says.
    /// </summary>
    private void AnnounceArrivals()
    {
        var state = App.Daemon.State;
        if (state.Connection != ConnectionState.Connected)
        {
            _announcingFor = "";
            _here.Clear();
            return;
        }

        var arriving = _announcingFor == state.GroupId;
        if (!arriving)
        {
            _announcingFor = state.GroupId ?? "";
            _here.Clear();
        }

        foreach (var peer in state.Peers)
        {
            // Connecting counts. Somebody whose link is still being built is
            // here, and waiting for it to open would hold the news back for
            // however long their router takes.
            if (peer.State == PeerState.Offline || !_here.Add(peer.DeviceId))
            {
                continue;
            }
            if (arriving)
            {
                Tray.ShowNotification($"{peer.Nickname} is here", $"On {state.GroupName}.");
            }
        }

        // Dropping whoever left is what lets them be announced again when they
        // come back, which for somebody whose connection is poor is the news.
        _here.RemoveWhere(id => !state.Peers.Any(p => p.DeviceId == id && p.State != PeerState.Offline));
    }

    /// <summary>
    /// Brings the window back from the tray. Showing is not enough on its own:
    /// a window restored behind whatever is in front of it reads as a click
    /// that did nothing.
    /// </summary>
    [RelayCommand]
    public void ShowWindow()
    {
        AppWindow.Show();
        Activate();
    }

    /// <summary>
    /// Opens the join screen on a link somebody clicked. It does not join for
    /// them: the screen names the group first.
    /// </summary>
    public void OpenInvite(string invite)
    {
        ShowWindow();
        ContentFrame.Navigate(typeof(GroupPage), new GroupPage.Invitation(invite));
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
    /// Quits the app. The service is left running: it is what holds the tunnels
    /// up, and stopping it is a decision taken in Settings rather than a side
    /// effect of closing a window.
    ///
    /// A command rather than a Click handler. The tray's flyout lives in its own
    /// window, and a Click on an item in there never reached this code, so the
    /// menu did nothing at all. The left-click binding always worked, which is
    /// what says commands are the way across that boundary.
    ///
    /// Synchronous, because an async command is disabled while it runs: a quit
    /// waiting on something slow greys Exit out until it finishes, and one
    /// waiting on something that never returns greys it out for good.
    /// </summary>
    [RelayCommand]
    private void ExitApp() => Quit();

    private void Quit()
    {
        _exiting = true;
        App.Daemon.StateChanged -= UpdateTray;
        App.Daemon.StateChanged -= AnnounceArrivals;

        // Taking the icon down by hand is worth trying, since it outlives the
        // process otherwise, but nothing here is worth staying open for.
        try
        {
            Tray.Dispose();
            App.Daemon.Shutdown();
        }
        catch (Exception error)
        {
            App.Trace($"tray exit: {error}");
        }

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
            // From the parameter, not the page: the page sets its own title in
            // OnNavigatedTo, which has not run by the time this does.
            ManageGroupPage => ManageGroupPage.TitleFor(e.Parameter),
            GroupPage page => page.ViewModel.Title,
            _ => null,
        };

        PageTitle.Text = title ?? "";
        PageTitle.Visibility = Show(title is not null);
        Wordmark.Visibility = Show(title is null);
        SettingsButton.Visibility = Show(title is null);
        BackButton.Visibility = Show(ContentFrame.CanGoBack);

        LayOutDragRegions();
    }

    private static Visibility Show(bool visible) => visible ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>
    /// Marks the gaps between the header's buttons as places the window can be
    /// dragged by. With no title bar of its own nothing up here drags until it
    /// is named, and a region over a button swallows its clicks.
    /// </summary>
    private void LayOutDragRegions()
    {
        if (Content?.XamlRoot is null || TitleBarRow.ActualWidth == 0)
        {
            return;
        }

        var scale = Content.XamlRoot.RasterizationScale;
        var height = TitleBarRow.ActualHeight;

        List<RectInt32> caption = [];
        var left = 0.0;

        // In the order they sit across the row, so each closes off the gap that
        // ran up to it. A button that is not there is space to drag by.
        foreach (var button in new FrameworkElement[] { BackButton, SettingsButton, MinimizeButton, CloseButton })
        {
            if (button.Visibility != Visibility.Visible || button.ActualWidth == 0)
            {
                continue;
            }

            var bounds = button
                .TransformToVisual(TitleBarRow)
                .TransformBounds(new Rect(0, 0, button.ActualWidth, button.ActualHeight));

            AddCaption(left, bounds.Left);
            left = bounds.Right;
        }
        AddCaption(left, TitleBarRow.ActualWidth);

        InputNonClientPointerSource
            .GetForWindowId(AppWindow.Id)
            .SetRegionRects(NonClientRegionKind.Caption, [.. caption]);

        // Full height, so the strip above and below a short button still drags.
        void AddCaption(double from, double to)
        {
            if (to - from < 1)
            {
                return;
            }

            caption.Add(new RectInt32(
                (int)(from * scale),
                0,
                (int)((to - from) * scale),
                (int)(height * scale)));
        }
    }

    private void OnMinimize(object sender, RoutedEventArgs e)
    {
        if (AppWindow.Presenter is OverlappedPresenter presenter)
        {
            presenter.Minimize();
        }
    }

    /// <summary>
    /// Hides, like every other way of closing this window does. Window.Close
    /// does not raise AppWindow's Closing, so the handler above never ran and
    /// this button quit the app outright.
    /// </summary>
    private void OnClose(object sender, RoutedEventArgs e) => AppWindow.Hide();

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
