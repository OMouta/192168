using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Net192168.Client.Ipc;

namespace Net192168.Client.ViewModels;

/// <summary>
/// The settings screen. One address is all a user ever sets: the client reads
/// everything else from the server's discovery document.
/// </summary>
public sealed partial class SettingsViewModel : ObservableObject
{
    private readonly Daemon _daemon;

    public SettingsViewModel(Daemon daemon)
    {
        _daemon = daemon;
        ServerUrl = "";
    }

    [ObservableProperty]
    public partial string? ServerUrl { get; set; }

    /// <summary>
    /// One line under the field, reporting what happened. It is empty until
    /// something does, and a test result is not worth a second dialog.
    /// </summary>
    [ObservableProperty]
    public partial string? Status { get; set; }

    [ObservableProperty]
    public partial bool IsBusy { get; set; }

    [RelayCommand]
    public async Task LoadAsync()
    {
        if (!_daemon.IsAvailable)
        {
            Status = "The background service is not running.";
            return;
        }
        try
        {
            var server = await _daemon.GetServerAsync();
            ServerUrl = server.Url;
        }
        catch (DaemonException e)
        {
            Status = ErrorCopy.Describe(e);
        }
    }

    /// <summary>
    /// Checks an address before it is saved. A server that cannot be reached is
    /// an answer to the question rather than an error.
    /// </summary>
    [RelayCommand]
    public async Task TestAsync()
    {
        IsBusy = true;
        try
        {
            var result = await _daemon.TestServerAsync((ServerUrl ?? "").Trim());
            Status = result.Reachable
                ? "That server works."
                : result.Message ?? "That server could not be reached.";
        }
        catch (DaemonException e)
        {
            Status = ErrorCopy.Describe(e);
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>
    /// Saves the address. Switching servers disconnects and registers again,
    /// because a device credential is only good where it was issued.
    /// </summary>
    /// <returns>Whether the dialog should close.</returns>
    [RelayCommand]
    public async Task<bool> SaveAsync()
    {
        IsBusy = true;
        try
        {
            await _daemon.SetServerAsync((ServerUrl ?? "").Trim());
            return true;
        }
        catch (DaemonException e)
        {
            Status = ErrorCopy.Describe(e);
            return false;
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>
    /// Puts settings back to how the app shipped. The daemon owns the defaults,
    /// so it decides what that means rather than the client guessing.
    /// </summary>
    [RelayCommand]
    public async Task ResetAsync()
    {
        IsBusy = true;
        try
        {
            var server = await _daemon.ResetSettingsAsync();
            ServerUrl = server.Url;
            Status = "Settings are back to their defaults.";
        }
        catch (DaemonException e)
        {
            Status = ErrorCopy.Describe(e);
        }
        finally
        {
            IsBusy = false;
        }
    }
}
