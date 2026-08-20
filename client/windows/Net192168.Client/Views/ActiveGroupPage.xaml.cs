using Microsoft.UI.Xaml.Controls;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

public sealed partial class ActiveGroupPage : Page
{
    public ActiveGroupPage()
    {
        InitializeComponent();
        ViewModel = new ActiveGroupViewModel(App.Daemon);
    }

    public ActiveGroupViewModel ViewModel { get; }
}
