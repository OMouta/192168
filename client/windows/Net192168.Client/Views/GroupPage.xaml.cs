using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Navigation;
using Windows.System;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

public enum GroupPageMode
{
    Create,
    Join,
}

/// <summary>
/// Creating and joining a group, as a screen rather than a popup.
///
/// In a window this narrow a dialog covers everything behind it anyway, so it
/// bought nothing and cost the rules about opening two at once, which have
/// already taken the app down.
/// </summary>
public sealed partial class GroupPage : Page
{
    public GroupPage()
    {
        InitializeComponent();
        ViewModel = new GroupViewModel(App.Daemon, creating: true);
    }

    public GroupViewModel ViewModel { get; private set; }

    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);

        // The mode decides every word on the screen, including the name the
        // window header shows, so it has to be in place before anything binds.
        var creating = e.Parameter is not GroupPageMode.Join;
        ViewModel = new GroupViewModel(App.Daemon, creating);
        Bindings.Update();

        if (creating)
        {
            NameBox.Focus(FocusState.Programmatic);
        }
        else
        {
            InviteBox.Focus(FocusState.Programmatic);
        }
    }

    /// <summary>
    /// Enter submits, which is what the key means on a form. Escape backs out,
    /// and that is the window header's accelerator rather than this screen's.
    /// </summary>
    private async void OnKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == VirtualKey.Enter && ViewModel.CanSubmit)
        {
            e.Handled = true;
            await SubmitAsync();
        }
    }

    private void OnDismissError(InfoBar sender, object args) => ViewModel.Error = null;

    private async void OnSubmit(object sender, RoutedEventArgs e) => await SubmitAsync();

    // Creating does not leave: it has a link to hand over, and that screen is
    // what Done backs out of.
    private void OnDone(object sender, RoutedEventArgs e) => GoBack();

    private async Task SubmitAsync()
    {
        if (await ViewModel.SubmitAsync())
        {
            GoBack();
        }
    }

    private void GoBack()
    {
        if (Frame.CanGoBack)
        {
            Frame.GoBack();
        }
    }
}
