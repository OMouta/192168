using Microsoft.UI.Xaml.Controls;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

/// <summary>
/// Settings is one field, so it is a dialog rather than a screen. Giving it a
/// permanent place in a window this small would cost more than it is worth.
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
            // Saving can fail, and closing on a failure would hide why.
            var deferral = args.GetDeferral();
            args.Cancel = !await ViewModel.SaveAsync();
            deferral.Complete();
        };

        SecondaryButtonClick += async (_, args) =>
        {
            // Reset leaves the dialog open so the result is visible in the
            // field and in the line under it.
            args.Cancel = true;
            var deferral = args.GetDeferral();
            await ViewModel.ResetAsync();
            deferral.Complete();
        };
    }

    public SettingsViewModel ViewModel { get; }
}
