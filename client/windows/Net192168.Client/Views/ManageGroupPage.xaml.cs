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
    /// The group is passed in rather than read back, because this screen is
    /// reached from one that already knows which group is connected.
    /// </summary>
    public sealed record Target(string GroupId, string Name);

    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);

        var target = e.Parameter as Target ?? new Target("", "");
        ViewModel = new ManageGroupViewModel(App.Daemon, target.GroupId, target.Name);
        Bindings.Update();
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
