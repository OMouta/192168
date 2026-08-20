using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Net192168.Client.Views;

namespace Net192168.Client;

public sealed partial class MainWindow : Window
{
    public MainWindow()
    {
        InitializeComponent();
        ContentFrame.Navigate(typeof(GroupsPage));

        // Every screen depends on the daemon, so say so once at the top rather
        // than letting each page fail its own way.
        App.Daemon.StateChanged += UpdateDaemonWarning;
        UpdateDaemonWarning();
    }

    private void UpdateDaemonWarning()
    {
        DaemonWarning.IsOpen = !App.Daemon.IsAvailable;
    }

    private void OnNavSelectionChanged(NavigationView sender, NavigationViewSelectionChangedEventArgs args)
    {
        if (args.SelectedItem is not NavigationViewItem item)
        {
            return;
        }

        var page = item.Tag switch
        {
            "active" => typeof(ActiveGroupPage),
            "settings" => typeof(SettingsPage),
            _ => typeof(GroupsPage),
        };

        ContentFrame.Navigate(page);
    }
}
