using Microsoft.UI.Xaml.Controls;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

/// <summary>
/// Settings is one field, so it is a dialog rather than a screen. Giving it a
/// permanent place in the window would cost more than it is worth.
/// </summary>
public sealed partial class SettingsDialog : ContentDialog
{
    public SettingsDialog()
    {
        InitializeComponent();
        ViewModel = new SettingsViewModel(App.Daemon);
        Loaded += async (_, _) => await ViewModel.LoadAsync();
        PrimaryButtonClick += async (_, args) =>
        {
            var deferral = args.GetDeferral();
            await ViewModel.SaveAsync();
            deferral.Complete();
        };
    }

    public SettingsViewModel ViewModel { get; }
}
