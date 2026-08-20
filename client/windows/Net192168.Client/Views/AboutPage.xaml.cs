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
    /// Opens the folder with the log picked out, rather than the log itself:
    /// the daemon writes its own alongside, and both are usually wanted.
    /// </summary>
    private void OnShowLog(object sender, RoutedEventArgs e)
    {
        try
        {
            var folder = Path.GetDirectoryName(App.LogPath)!;
            Directory.CreateDirectory(folder);

            var target = File.Exists(App.LogPath) ? $"/select,\"{App.LogPath}\"" : $"\"{folder}\"";
            Process.Start(new ProcessStartInfo("explorer.exe", target) { UseShellExecute = true });
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            App.Trace($"could not open the log folder: {error.Message}");
        }
    }
}
