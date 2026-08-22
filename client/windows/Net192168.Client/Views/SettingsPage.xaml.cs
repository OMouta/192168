using System.Diagnostics;
using System.IO;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;
using Net192168.Client.Ipc;
using Net192168.Client.Services;
using Net192168.Client.ViewModels;
using Windows.Storage.Pickers;

namespace Net192168.Client.Views;

/// <summary>
/// Settings, as a screen rather than a popup.
///
/// It is one field, which is what made a dialog defensible, but a dialog in a
/// window this narrow hides everything behind it and brings the rules about
/// opening two at once along with it.
/// </summary>
public sealed partial class SettingsPage : Page
{
    /// <summary>
    /// The tabs, in the order they appear. Named so the two places that care
    /// which one is showing do not both count positions by hand.
    /// </summary>
    private enum Tab
    {
        General,
        Network,
        Updates,
        App,
    }

    public SettingsPage()
    {
        InitializeComponent();
        ViewModel = new SettingsViewModel(App.Daemon);
    }

    public SettingsViewModel ViewModel { get; }

    /// <summary>The update, shared with the banner on home.</summary>
    public UpdateViewModel Update => UpdateViewModel.Current;

    protected override async void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        await ViewModel.LoadAsync();
    }

    /// <summary>
    /// Shows one tab and hides the rest, and puts the Save bar up only where
    /// there is something to save.
    /// </summary>
    private void OnTabChanged(SelectorBar sender, SelectorBarSelectionChangedEventArgs args)
    {
        var tab = (Tab)sender.Items.IndexOf(sender.SelectedItem);

        GeneralTab.Visibility = Show(tab is Tab.General);
        NetworkTab.Visibility = Show(tab is Tab.Network);
        UpdatesTab.Visibility = Show(tab is Tab.Updates);
        AppTab.Visibility = Show(tab is Tab.App);

        // Your name and the server address are the only two things here that
        // wait to be saved. Everything else applies as it is touched, and a
        // Save button over a tab with nothing to save reads as a step missed.
        SaveBar.Visibility = Show(tab is Tab.General or Tab.Network);

        // Each tab starts at its own top rather than wherever the last one was
        // left.
        TabScroll.ChangeView(null, 0, null, disableAnimation: true);

        if (tab is Tab.Updates)
        {
            // Opening this tab is somebody asking, so it goes and looks even
            // when the app is set not to on its own.
            if (Update.CheckCommand.CanExecute(null))
            {
                Update.CheckCommand.Execute(null);
            }
        }
    }

    private static Visibility Show(bool visible) => visible ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>
    /// Saving that fails keeps the screen, so the reason stays next to the
    /// address that caused it.
    /// </summary>
    private async void OnSave(object sender, RoutedEventArgs e)
    {
        if (await ViewModel.SaveAsync() && Frame.CanGoBack)
        {
            Frame.GoBack();
        }
    }

    // Reset stays on the screen so the result is visible in the field and in
    // the line under it.
    private async void OnReset(object sender, RoutedEventArgs e) => await ViewModel.ResetAsync();

    private void OnAbout(object sender, RoutedEventArgs e) => Frame.Navigate(typeof(AboutPage));

    /// <summary>
    /// Opens the folder holding the logs, with the daemon's picked out. That is
    /// the one worth sending when a tunnel will not come up; the others sit
    /// beside it.
    /// </summary>
    private void OnShowLogs(object sender, RoutedEventArgs e)
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
            var packetLog = ViewModel.PacketLog;

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
    /// Puts a line under the log row. The app has no dialogs, and a result that
    /// needs dismissing would be the first one.
    /// </summary>
    private void Say(string message)
    {
        LogStatus.Text = message;
        LogStatus.Visibility = Visibility.Visible;
    }
}
