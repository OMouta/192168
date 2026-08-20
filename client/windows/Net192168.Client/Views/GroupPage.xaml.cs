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

        // The mode decides every word on the screen, so it has to be in place
        // before anything binds.
        var creating = e.Parameter is not GroupPageMode.Join;
        ViewModel = new GroupViewModel(App.Daemon, creating);
        Bindings.Update();

        NameBox.Focus(FocusState.Programmatic);
    }

    private void OnPasswordChanged(object sender, RoutedEventArgs e)
        => ViewModel.Password = PasswordInput.Password;

    /// <summary>
    /// Enter submits and Escape backs out, which is what the dialog this
    /// replaced did and what the keys mean on a form.
    /// </summary>
    private async void OnKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == VirtualKey.Enter && ViewModel.CanSubmit)
        {
            e.Handled = true;
            await SubmitAsync();
        }
        else if (e.Key == VirtualKey.Escape)
        {
            e.Handled = true;
            GoBack();
        }
    }

    private void OnBack(object sender, RoutedEventArgs e) => GoBack();

    private async void OnSubmit(object sender, RoutedEventArgs e) => await SubmitAsync();

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
