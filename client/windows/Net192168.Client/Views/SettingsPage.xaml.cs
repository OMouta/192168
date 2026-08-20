using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

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
}
