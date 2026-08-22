using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

/// <summary>
/// What the owner of a group can change about the group itself.
///
/// The people in it are managed from their own rows on the screen before this
/// one, where they are already listed and already named.
/// </summary>
public sealed partial class ManageGroupPage : Page
{
    public ManageGroupPage() => InitializeComponent();

    public ManageGroupViewModel ViewModel { get; private set; } = null!;

    /// <summary>
    /// What the window header says. The name rather than "Group": there can be
    /// several groups, and this screen changes exactly one of them.
    ///
    /// Read off the navigation parameter, because the header is set while
    /// navigating and this page has not been given the group yet.
    /// </summary>
    public static string TitleFor(object? parameter)
        => parameter is Target target && target.Name != "" ? target.Name + " settings" : "Group";

    /// <summary>
    /// The group is passed in rather than read back, because this screen is
    /// reached from one that already lists every group it could be about.
    /// </summary>
    public sealed record Target(string GroupId, string Name, string? Icon = null, string? Color = null, string? InviteLink = null);

    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);

        var target = e.Parameter as Target ?? new Target("", "");
        ViewModel = new ManageGroupViewModel(App.Daemon, target.GroupId, target.Name, target.Icon, target.Color, target.InviteLink);
        Bindings.Update();
    }

    /// <summary>
    /// Deleting leaves nothing to come back to, so the screen goes with it. Home
    /// is where the groups list is, and this group is no longer on it.
    /// </summary>
    private async void OnDelete(object sender, RoutedEventArgs e)
    {
        if (await ViewModel.DeleteAsync() && Frame.CanGoBack)
        {
            Frame.GoBack();
        }
    }

    /// <summary>
    /// A save that fails keeps the screen, so the reason stays next to the field
    /// that caused it and nothing typed is lost.
    /// </summary>
    private async void OnSave(object sender, RoutedEventArgs e)
    {
        if (await ViewModel.SaveAsync() && Frame.CanGoBack)
        {
            Frame.GoBack();
        }
    }
}
