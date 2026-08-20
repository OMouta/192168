using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Net192168.Client.Views;
using Windows.Graphics;

namespace Net192168.Client;

public sealed partial class MainWindow : Window
{
    // A group is under ten people and every row is one line, so the window only
    // has to be tall enough for that list. Anything wider is empty space beside
    // a game.
    private const int Width = 380;
    private const int Height = 520;

    public MainWindow()
    {
        InitializeComponent();

        ExtendsContentIntoTitleBar = true;
        SetTitleBar(TitleBar);

        AppWindow.Title = "192168";
        AppWindow.Resize(new SizeInt32(Width, Height));

        // The caption buttons draw themselves, so they have to be told not to
        // paint a light strip across the top of a dark window.
        AppWindow.TitleBar.ButtonBackgroundColor = Colors.Transparent;
        AppWindow.TitleBar.ButtonInactiveBackgroundColor = Colors.Transparent;
        AppWindow.TitleBar.ButtonForegroundColor = Colors.White;
        AppWindow.TitleBar.ButtonInactiveForegroundColor = ColorHelper.FromArgb(0xFF, 0x8A, 0x90, 0xA0);
        AppWindow.TitleBar.ButtonHoverBackgroundColor = ColorHelper.FromArgb(0xFF, 0x28, 0x2C, 0x36);
        AppWindow.TitleBar.ButtonHoverForegroundColor = Colors.White;
        AppWindow.TitleBar.ButtonPressedBackgroundColor = ColorHelper.FromArgb(0xFF, 0x1B, 0x1E, 0x26);
        AppWindow.TitleBar.ButtonPressedForegroundColor = Colors.White;

        if (AppWindow.Presenter is OverlappedPresenter presenter)
        {
            // Maximising a window this dense would only stretch whitespace.
            presenter.IsMaximizable = false;
            presenter.PreferredMinimumWidth = Width;
            presenter.PreferredMinimumHeight = 360;
        }

        ContentFrame.Navigate(typeof(HomePage));

        // The caption buttons sit on top of the title bar, so the settings
        // button has to be told how much room they take. It changes with DPI
        // and with the buttons appearing, so it is read rather than guessed.
        TitleBar.SizeChanged += (_, _) => ReserveCaptionSpace();
        Activated += (_, _) => ReserveCaptionSpace();
    }

    private void ReserveCaptionSpace()
    {
        var scale = Content.XamlRoot?.RasterizationScale ?? 1.0;
        CaptionSpacer.Width = new GridLength(AppWindow.TitleBar.RightInset / scale);
    }

    private async void OnSettings(object sender, RoutedEventArgs e)
    {
        var dialog = new SettingsDialog { XamlRoot = Content.XamlRoot };
        await dialog.ShowAsync();
    }
}
