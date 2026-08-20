using System.Diagnostics;
using System.IO;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Net192168.Client.Views;

/// <summary>
/// What this build is, and where to go from here.
/// </summary>
public sealed partial class AboutPage : Page
{
    public AboutPage() => InitializeComponent();

    public string Version => $"Version {AppInfo.Version}";

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
