using Net192168.Client.Views;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Net192168.Client;

public sealed partial class MainWindow : Window
{
    public MainWindow()
    {
        InitializeComponent();
        ContentFrame.Navigate(typeof(GroupsPage));
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
