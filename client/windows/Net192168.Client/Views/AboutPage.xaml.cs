using System.Diagnostics;
using System.IO;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Net192168.Client.Ipc;
using Net192168.Client.Services;
using Net192168.Client.ViewModels;
using Windows.Storage.Pickers;

namespace Net192168.Client.Views;

/// <summary>
/// What this build is, and where to go from here.
/// </summary>
public sealed partial class AboutPage : Page
{
    public AboutPage() => InitializeComponent();

    public string Version => $"Version {AppInfo.Version}";

    /// <summary>The update, shared with the banner on home.</summary>
    public UpdateViewModel Update => UpdateViewModel.Current;

    /// <summary>
    /// Opens the folder holding the logs, with the daemon's picked out. That is
    /// the one worth sending when a tunnel will not come up; the others sit
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
            Say("Could not open the log folder.");
        }
    }

    /// <summary>
    /// Packs every log into one zip, wherever the user says to put it.
    ///
    /// One file to attach beats four to find, and the machine details that go in
    /// with them are the first thing anybody reading a report asks for.
    /// </summary>
    private async void OnExportLogs(object sender, RoutedEventArgs e)
    {
        ExportLogs.IsEnabled = false;
        try
        {
            var picker = new FileSavePicker
            {
                SuggestedStartLocation = PickerLocationId.Downloads,
                SuggestedFileName = LogExport.SuggestedName,
            };
            picker.FileTypeChoices.Add("Zip archive", [".zip"]);

            // The app is unpackaged, so a picker has no window of its own to
            // hang off and has to be told which one it belongs to.
            WinRT.Interop.InitializeWithWindow.Initialize(
                picker, WinRT.Interop.WindowNative.GetWindowHandle(App.Window));

            var file = await picker.PickSaveFileAsync();
            if (file is null)
            {
                // Cancelled, which is not a failure and not worth a line.
                return;
            }

            Say("Collecting…");
            var packetLog = await PacketLogEnabledAsync();

            // Read here rather than inside the copy: the daemon replaces this
            // whole record on the UI thread as events arrive, and the export
            // should describe one moment rather than whichever it caught.
            var state = App.Daemon.State;

            var written = await Task.Run(() => LogExport.WriteAsync(file.Path, state, packetLog));

            Say(written == 0
                ? "Saved, but there were no logs to put in it yet."
                : $"Saved {written} log{(written == 1 ? "" : "s")} to {file.Name}.");
        }
        catch (Exception error)
        {
            App.Trace($"could not export the logs: {error}");
            Say("Could not save the logs.");
        }
        finally
        {
            ExportLogs.IsEnabled = true;
        }
    }

    /// <summary>
    /// Empties every log.
    ///
    /// The daemon's are the daemon's to clear: it holds them open as a service,
    /// and deleting one from here would appear to work while leaving it writing
    /// to a file nobody can read. This app's own is cleared here.
    /// </summary>
    private async void OnClearLogs(object sender, RoutedEventArgs e)
    {
        ClearLogs.IsEnabled = false;
        try
        {
            App.ClearLog();

            // Asked for after, so a daemon that cannot be reached still leaves
            // this app's log cleared rather than nothing at all.
            var cleared = await App.Daemon.ClearLogsAsync();
            Say($"Cleared {cleared.Cleared.Count + 1} logs.");
        }
        catch (DaemonException error)
        {
            App.Trace($"could not clear the daemon logs: {error.Message}");
            Say("Cleared this app's log. The daemon's could not be cleared.");
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            App.Trace($"could not clear the logs: {error.Message}");
            Say("Could not clear the logs.");
        }
        finally
        {
            ClearLogs.IsEnabled = true;
        }
    }

    /// <summary>
    /// Whether the packet log is running, which belongs in the export because a
    /// missing packets.log otherwise reads as one that was lost.
    /// </summary>
    private static async Task<bool> PacketLogEnabledAsync()
    {
        try
        {
            return (await App.Daemon.GetPacketLogAsync()).Enabled;
        }
        catch (DaemonException)
        {
            // An older daemon, or one that is not answering. Not knowing is not
            // a reason to refuse the export.
            return false;
        }
    }

    /// <summary>
    /// Puts a line under the row. The app has no dialogs, and a result that
    /// needs dismissing would be the first.
    /// </summary>
    private void Say(string message) => LogStatus.Text = message;
}
