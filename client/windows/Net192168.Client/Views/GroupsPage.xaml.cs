using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;
using Net192168.Client.Ipc;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

public sealed partial class GroupsPage : Page
{
    public GroupsPage()
    {
        InitializeComponent();
        ViewModel = new GroupsViewModel(App.Daemon);
    }

    public GroupsViewModel ViewModel { get; }

    protected override async void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        await ViewModel.RefreshAsync();
    }

    private async void OnToggle(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: GroupListItem item })
        {
            await ViewModel.ToggleAsync(item);
        }
    }

    private async void OnCreate(object sender, RoutedEventArgs e)
    {
        var dialog = new GroupDialog(GroupDialogMode.Create) { XamlRoot = XamlRoot };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }

        try
        {
            await ViewModel.CreateAsync(dialog.GroupName, dialog.Password, dialog.Nickname);
        }
        catch (DaemonException error)
        {
            await ShowError(error.Message);
        }
    }

    private async void OnJoin(object sender, RoutedEventArgs e)
    {
        var dialog = new GroupDialog(GroupDialogMode.Join) { XamlRoot = XamlRoot };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }

        try
        {
            await ViewModel.JoinAsync(dialog.GroupName, dialog.Password, dialog.Nickname);
        }
        catch (DaemonException error)
        {
            await ShowError(error.Message);
        }
    }

    private Task ShowError(string message) => new ContentDialog
    {
        XamlRoot = XamlRoot,
        Title = "That did not work",
        Content = message,
        CloseButtonText = "OK",
    }.ShowAsync().AsTask();
}
