using CommunityToolkit.Mvvm.Input;
using Microsoft.UI;
using Microsoft.UI.Input;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Net192168.Client.Ipc;
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

        // The system title bar follows the system theme rather than the app's,
        // so it comes out light on most machines. Extending into it keeps the
        // top of the window dark and puts the wordmark level with the window
        // buttons.
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(TitleBarRow);

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

        SettingsButton.Loaded += (_, _) => CutSettingsButtonOutOfDragRegion();
        SettingsButton.SizeChanged += (_, _) => CutSettingsButtonOutOfDragRegion();

        // The gear belongs to the home screen. On any other screen it would
        // either do nothing or stack a second settings page on the first.
        ContentFrame.Navigated += (_, _) =>
            SettingsButton.Visibility = ContentFrame.CurrentSourcePageType == typeof(HomePage)
                ? Visibility.Visible
                : Visibility.Collapsed;

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

    private void OnTrayOpen(object sender, RoutedEventArgs e) => ShowWindow();

    private async void OnTrayDisconnect(object sender, RoutedEventArgs e)
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

    private void OnTrayExit(object sender, RoutedEventArgs e)
    {
        _exiting = true;
        App.Daemon.StateChanged -= UpdateTray;

        // The icon outlives the process unless it is taken down by hand, which
        // leaves a dead entry in the tray until something hovers over it.
        Tray.Dispose();
        Application.Current.Exit();
    }

    /// <summary>
    /// Marks the settings button as somewhere the window should not treat as a
    /// title bar.
    ///
    /// Everything inside the element given to SetTitleBar is a drag handle, and
    /// a button in there receives input it should not: the first version of this
    /// opened the settings dialog by itself when the window was activated.
    /// </summary>
    private void CutSettingsButtonOutOfDragRegion()
    {
        if (Content?.XamlRoot is null)
        {
            return;
        }

        var scale = Content.XamlRoot.RasterizationScale;
        var bounds = SettingsButton
            .TransformToVisual(Content)
            .TransformBounds(new Rect(0, 0, SettingsButton.ActualWidth, SettingsButton.ActualHeight));

        var rect = new RectInt32(
            (int)(bounds.X * scale),
            (int)(bounds.Y * scale),
            (int)(bounds.Width * scale),
            (int)(bounds.Height * scale));

        InputNonClientPointerSource
            .GetForWindowId(AppWindow.Id)
            .SetRegionRects(NonClientRegionKind.Passthrough, [rect]);
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
