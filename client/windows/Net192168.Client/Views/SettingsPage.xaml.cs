using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

/// <summary>
/// Settings, as a screen rather than a popup.
///
/// It is one field, which is what made a dialog defensible, but a dialog in a
/// window this narrow hides everything behind it and brings the rules about
/// opening two at once along with it.
/// </summary>
public sealed partial class SettingsPage : Page
{
    public SettingsPage()
    {
        InitializeComponent();
        ViewModel = new SettingsViewModel(App.Daemon);
    }

    public SettingsViewModel ViewModel { get; }

    protected override async void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        await ViewModel.LoadAsync();
    }

    /// <summary>
    /// Saving that fails keeps the screen, so the reason stays next to the
    /// address that caused it.
    /// </summary>
    private async void OnSave(object sender, RoutedEventArgs e)
    {
        if (await ViewModel.SaveAsync() && Frame.CanGoBack)
        {
            Frame.GoBack();
        }
    }

    // Reset stays on the screen so the result is visible in the field and in
    // the line under it.
    private async void OnReset(object sender, RoutedEventArgs e) => await ViewModel.ResetAsync();

    private void OnAbout(object sender, RoutedEventArgs e) => Frame.Navigate(typeof(AboutPage));
}
