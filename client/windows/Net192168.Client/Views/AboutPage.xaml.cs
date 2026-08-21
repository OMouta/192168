using System.Diagnostics;
using System.IO;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Net192168.Client.Services;

namespace Net192168.Client.Views;

/// <summary>
/// What this build is, and where to go from here.
/// </summary>
public sealed partial class AboutPage : Page
{
    public AboutPage() => InitializeComponent();

    public string Version => $"Version {AppInfo.Version}";

    /// <summary>Whether there is a newer release to go and get.</summary>
    public bool HasUpdate => Updates.Available is not null;

    public string UpdateLabel => $"Download {Updates.Available?.Version}";

    /// <summary>
    /// The release page, opened in a browser. Downloading and installing an app
    /// that installed a service is a bigger idea than this needs.
    /// </summary>
    public Uri UpdateUrl => new(Updates.Available?.Url ?? "https://github.com/OMouta/192168/releases");

    /// <summary>
    /// Opens the folder holding both logs, with the daemon's picked out. That
    /// is the one worth sending when a tunnel will not come up; this app's sits
    /// beside it.
    /// </summary>
    private void OnShowLog(object sender, RoutedEventArgs e)
    {
        try
        {
            Directory.CreateDirectory(App.LogFolder);

            var daemon = Path.Combine(App.LogFolder, "daemon.log");
            var pick = File.Exists(daemon) ? daemon : App.LogPath;

            var target = File.Exists(pick) ? $"/select,\"{pick}\"" : $"\"{App.LogFolder}\"";
            Process.Start(new ProcessStartInfo("explorer.exe", target) { UseShellExecute = true });
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            App.Trace($"could not open the log folder: {error.Message}");
        }
    }
}
