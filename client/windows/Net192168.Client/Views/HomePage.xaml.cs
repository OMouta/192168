using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Navigation;
using Windows.System;
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

    /// <summary>Fetches the group list again, for the window coming back from
    /// the tray, which is not a navigation.</summary>
    public Task RefreshAsync() => ViewModel.RefreshAsync();

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
            // Handled here so it stops before the header's back accelerator.
            e.Handled = true;
            ViewModel.CancelEditingNicknameCommand.Execute(null);
        }
    }

    private async void OnNicknameLostFocus(object sender, RoutedEventArgs e)
        => await ViewModel.CommitNicknameAsync();

    private async void OnConnect(object sender, RoutedEventArgs e)
    {
        if (Group(sender) is GroupListItem item)
        {
            await ViewModel.ConnectAsync(item);
        }
    }

    private void OnStartLeaving(object sender, RoutedEventArgs e)
        => ViewModel.StartLeavingCommand.Execute(Group(sender));

    private void OnCancelLeaving(object sender, RoutedEventArgs e)
        => ViewModel.CancelLeavingCommand.Execute(Group(sender));

    private async void OnLeave(object sender, RoutedEventArgs e)
    {
        if (Group(sender) is GroupListItem item)
        {
            await ViewModel.LeaveAsync(item);
        }
    }

    private void OnDismissMessage(InfoBar sender, object args) => ViewModel.Message = null;

    /// <summary>
    /// The group a row's button belongs to.
    ///
    /// A flyout lives in its own popup rather than under the row, so the group
    /// is carried on the item itself. DataContext is checked too, because that
    /// is what an element does inherit when it stays in the tree.
    /// </summary>
    private static GroupListItem? Group(object sender)
        => (sender as FrameworkElement)?.Tag as GroupListItem
            ?? (sender as FrameworkElement)?.DataContext as GroupListItem;

    // The group screen reports its own failures and comes back only once it has
    // worked, so there is nothing to catch here.
    private void OnCreate(object sender, RoutedEventArgs e)
        => Frame.Navigate(typeof(GroupPage), GroupPageMode.Create);

    private void OnJoin(object sender, RoutedEventArgs e)
        => Frame.Navigate(typeof(GroupPage), GroupPageMode.Join);

    // The name, the look, and the invite. Who is in the group is managed from
    // the rows themselves, where the people already are.
    //
    // From the connected group's row, which carries the look. A name alone
    // would put the picker back to the default.
    private void OnManageGroup(object sender, RoutedEventArgs e)
        => Frame.Navigate(typeof(ManageGroupPage), ViewModel.ConnectedGroup is GroupListItem group
            ? Target(group)
            : new ManageGroupPage.Target(ViewModel.ConnectedGroupId, ViewModel.GroupLabel ?? ""));

    // The same screen, reached from a group in the list rather than from the one
    // that is connected, so a group can be changed without joining it first.
    private void OnManageGroupFromList(object sender, RoutedEventArgs e)
    {
        if (Group(sender) is GroupListItem group)
        {
            Frame.Navigate(typeof(ManageGroupPage), Target(group));
        }
    }

    private static ManageGroupPage.Target Target(GroupListItem group)
        => new(group.GroupId, group.Name ?? "", group.Icon, group.Color, group.InviteLink);

    // Copying from the row itself, for the common case: the owner wants the
    // link and nothing else on the settings screen.
    private void OnCopyInvite(object sender, RoutedEventArgs e)
    {
        if (Group(sender) is GroupListItem group)
        {
            ViewModel.CopyInvite(group);
        }
    }

    // The same thing while a group is up, where the list of groups has given
    // way to the list of people and the row is not there to open.
    private void OnCopyConnectedInvite(object sender, RoutedEventArgs e)
    {
        if (ViewModel.ConnectedGroup is GroupListItem group)
        {
            ViewModel.CopyInvite(group);
        }
    }

    // Leaving the group that is connected. The daemon disconnects on the way.
    private async void OnLeaveConnected(object sender, RoutedEventArgs e)
        => await ViewModel.LeaveConnectedGroupAsync();
}
