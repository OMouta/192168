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

        // Home is returned to rather than visited, and its view model is
        // subscribed to the daemon for the life of the app. A fresh instance on
        // every trip back would leave the old one listening.
        NavigationCacheMode = NavigationCacheMode.Required;
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

    /// <summary>
    /// Leaves a group, after asking.
    ///
    /// The daemon never stores a group password, so getting back in means
    /// having it to hand again. That is worth a question, and the answer names
    /// the group so it is clear which one is going.
    /// </summary>
    private async void OnLeave(object sender, RoutedEventArgs e)
    {
        // The flyout lives in its own popup rather than under the row, so the
        // group is carried on the item itself. DataContext is checked too,
        // because that is what a flyout inherits when it does reach it.
        var item = (sender as FrameworkElement)?.Tag as GroupListItem
            ?? (sender as FrameworkElement)?.DataContext as GroupListItem;
        if (item is null)
        {
            return;
        }

        var confirm = new ContentDialog
        {
            XamlRoot = XamlRoot,
            Title = $"Leave {item.Name}?",
            Content = "You will need the group password to join again.",
            PrimaryButtonText = "Leave",
            CloseButtonText = "Cancel",
            DefaultButton = ContentDialogButton.Close,
        };

        if (await confirm.ShowAsync() == ContentDialogResult.Primary)
        {
            await ViewModel.LeaveAsync(item);
        }
    }

    // The group screen reports its own failures and comes back only once it has
    // worked, so there is nothing to catch here.
    private void OnCreate(object sender, RoutedEventArgs e)
        => Frame.Navigate(typeof(GroupPage), GroupPageMode.Create);

    private void OnJoin(object sender, RoutedEventArgs e)
        => Frame.Navigate(typeof(GroupPage), GroupPageMode.Join);
}
