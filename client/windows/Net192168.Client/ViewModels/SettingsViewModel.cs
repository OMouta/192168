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

    [ObservableProperty]
    public partial string? Result { get; set; }

    [ObservableProperty]
    public partial bool IsBusy { get; set; }

    [ObservableProperty]
    public partial bool IsFailure { get; set; }

    [RelayCommand]
    public async Task LoadAsync()
    {
        if (!_daemon.IsAvailable)
        {
            return;
        }
        try
        {
            var server = await _daemon.GetServerAsync();
            ServerUrl = server.Url;
        }
        catch (DaemonException e)
        {
            Report(e.Message, failure: true);
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
            if (result.Reachable)
            {
                Report("That server works.", failure: false);
            }
            else
            {
                Report(result.Message ?? "That server could not be reached.", failure: true);
            }
        }
        catch (DaemonException e)
        {
            Report(e.Message, failure: true);
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
    [RelayCommand]
    public async Task SaveAsync()
    {
        IsBusy = true;
        try
        {
            await _daemon.SetServerAsync((ServerUrl ?? "").Trim());
            Report("Saved.", failure: false);
        }
        catch (DaemonException e)
        {
            Report(e.Message, failure: true);
        }
        finally
        {
            IsBusy = false;
        }
    }

    private void Report(string message, bool failure)
    {
        Result = message;
        IsFailure = failure;
    }
}
