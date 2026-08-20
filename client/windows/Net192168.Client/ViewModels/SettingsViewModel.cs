using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Net192168.Client.Ipc;
using Net192168.Client.Services;

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

    /// <summary>What the service is doing, in a word.</summary>
    [ObservableProperty]
    public partial string ServiceHeadline { get; set; } = "";

    /// <summary>
    /// What that means for the user, under the headline. It also carries the
    /// reason when an action fails, so a service problem is explained where the
    /// service is rather than under the server address.
    /// </summary>
    [ObservableProperty]
    public partial string ServiceDetail { get; set; } = "";

    /// <summary>
    /// Whether the service is registered at all. It decides which of the two
    /// sets of buttons makes sense: install it, or stop and remove it.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsServiceMissing))]
    public partial bool IsServiceInstalled { get; set; }

    /// <summary>The other side of <see cref="IsServiceInstalled"/>, so Install
    /// and Remove can be the same button position without a converter.</summary>
    public bool IsServiceMissing => !IsServiceInstalled;

    /// <summary>Whether it is running right now.</summary>
    [ObservableProperty]
    public partial bool IsServiceRunning { get; set; }

    /// <summary>
    /// Whether there is a service to manage at all. False during development,
    /// where the daemon runs in a console and none of these buttons mean
    /// anything.
    /// </summary>
    [ObservableProperty]
    public partial bool CanManageService { get; set; }

    /// <summary>Whether the app opens when the user signs in.</summary>
    public bool StartsWithWindows
    {
        get => StartWithWindows.IsEnabled;
        set
        {
            if (StartWithWindows.IsEnabled == value)
            {
                return;
            }
            StartWithWindows.Set(value);
            OnPropertyChanged();
        }
    }

    [RelayCommand]
    public async Task LoadAsync()
    {
        await RefreshServiceAsync();

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

    /// <summary>Asks Windows what the service is doing and describes it.</summary>
    public async Task RefreshServiceAsync()
    {
        CanManageService = DaemonService.IsAvailable;
        if (!CanManageService)
        {
            IsServiceInstalled = false;
            IsServiceRunning = false;
            return;
        }

        var state = await DaemonService.QueryAsync();
        IsServiceInstalled = state is ServiceState.Stopped or ServiceState.Running or ServiceState.Pending;
        IsServiceRunning = state == ServiceState.Running;
        (ServiceHeadline, ServiceDetail) = state switch
        {
            ServiceState.Running => ("Running", "Stopping it disconnects you."),
            ServiceState.Stopped => ("Stopped", "It starts again when you connect."),
            ServiceState.Pending => ("Starting or stopping", "This takes a moment."),
            ServiceState.Absent => ("Not installed", "This machine cannot join a network without it."),
            _ => ("Unknown", "Its state could not be checked."),
        };
    }

    /// <summary>Stops the service. Needs no administrator rights, by design.</summary>
    [RelayCommand]
    public async Task StopServiceAsync()
    {
        IsBusy = true;
        try
        {
            // Success shows itself in the headline, so only a failure needs words.
            var stopped = await DaemonService.StopAsync();
            await RefreshServiceAsync();
            if (!stopped)
            {
                ServiceDetail = "It could not be stopped.";
            }
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>
    /// Registers the service. Prompts for administrator rights.
    /// </summary>
    [RelayCommand]
    public async Task InstallServiceAsync()
    {
        IsBusy = true;
        try
        {
            var installed = await DaemonService.InstallAsync();
            await RefreshServiceAsync();
            if (!installed)
            {
                ServiceDetail = "It could not be installed.";
            }
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>
    /// Removes the service and the network adapter, leaving the app installed.
    /// Prompts for administrator rights.
    /// </summary>
    [RelayCommand]
    public async Task RemoveServiceAsync()
    {
        IsBusy = true;
        try
        {
            var removed = await DaemonService.RemoveAsync();
            await RefreshServiceAsync();
            if (!removed)
            {
                ServiceDetail = "It could not be removed.";
            }
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>Opens the Windows list of installed apps.</summary>
    [RelayCommand]
    public void UninstallApp() => DaemonService.OpenWindowsUninstaller();

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
