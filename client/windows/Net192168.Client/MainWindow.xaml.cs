using Microsoft.UI;
using Microsoft.UI.Input;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
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

        ContentFrame.Navigate(typeof(HomePage));
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
