using Microsoft.UI.Xaml.Controls;

namespace Net192168.Client.Views;

/// <summary>
/// What this build is, and where to go from here.
///
/// The version and the two links, and nothing that does anything. Updating and
/// the logs are both settings, and both sat here only because this was the one
/// screen that was not about connecting.
/// </summary>
public sealed partial class AboutPage : Page
{
    public AboutPage() => InitializeComponent();

    public string Version => $"Version {AppInfo.Version}";
}
