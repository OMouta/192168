using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Navigation;
using Windows.System;
using Net192168.Client.Ipc;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

public sealed partial class HomePage : Page
{
    public HomePage()
    {
        InitializeComponent();
        ViewModel = new HomeViewModel(App.Daemon);
    }

    public HomeViewModel ViewModel { get; }

    protected override async void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        await ViewModel.RefreshAsync();
    }

    private void OnEditNickname(object sender, RoutedEventArgs e)
    {
        ViewModel.StartEditingNicknameCommand.Execute(null);
        NicknameBox.Focus(FocusState.Programmatic);
        NicknameBox.SelectAll();
    }

    private async void OnNicknameKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == VirtualKey.Enter)
        {
            e.Handled = true;
            await ViewModel.CommitNicknameAsync();
        }
        else if (e.Key == VirtualKey.Escape)
        {
            e.Handled = true;
            ViewModel.CancelEditingNicknameCommand.Execute(null);
        }
    }

    private async void OnNicknameLostFocus(object sender, RoutedEventArgs e)
        => await ViewModel.CommitNicknameAsync();

    private async void OnConnect(object sender, RoutedEventArgs e)
    {
        if (sender is Button { Tag: GroupListItem item })
        {
            await ViewModel.ConnectAsync(item);
        }
    }

    private async void OnCreate(object sender, RoutedEventArgs e)
        => await ShowGroupDialog(GroupDialogMode.Create);

    private async void OnJoin(object sender, RoutedEventArgs e)
        => await ShowGroupDialog(GroupDialogMode.Join);

    private async Task ShowGroupDialog(GroupDialogMode mode)
    {
        var dialog = new GroupDialog(mode) { XamlRoot = XamlRoot };
        if (await dialog.ShowAsync() != ContentDialogResult.Primary)
        {
            return;
        }

        try
        {
            if (mode == GroupDialogMode.Create)
            {
                await ViewModel.CreateAsync(dialog.GroupName, dialog.Password, dialog.Nickname);
            }
            else
            {
                await ViewModel.JoinAsync(dialog.GroupName, dialog.Password, dialog.Nickname);
            }
        }
        catch (DaemonException error)
        {
            await new ContentDialog
            {
                XamlRoot = XamlRoot,
                Title = mode == GroupDialogMode.Create ? "Could not create that group" : "Could not join that group",
                Content = error.Message,
                CloseButtonText = "OK",
            }.ShowAsync();
        }
    }
}
