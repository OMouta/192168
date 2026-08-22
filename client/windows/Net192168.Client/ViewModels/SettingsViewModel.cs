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
    /// What everyone in every group sees. It belongs to this machine, not to a
    /// group, so it can be changed without connecting to one.
    /// </summary>
    [ObservableProperty]
    public partial string? Nickname { get; set; }

    /// <summary>What the name was on arrival, so Save knows whether to send it.</summary>
    private string _savedNickname = "";

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

    /// <summary>
    /// Whether the app looks for a newer version when it opens. It is the only
    /// thing this app asks for that is not the coordination server.
    /// </summary>
    public bool ChecksForUpdates
    {
        get => Updates.Enabled;
        set
        {
            if (Updates.Enabled == value)
            {
                return;
            }
            Updates.Enabled = value;
            OnPropertyChanged();
        }
    }

    private bool _lanDiscovery = true;

    /// <summary>
    /// Whether the group is treated as a real LAN, so a game that only finds
    /// servers by scanning one finds the group.
    ///
    /// It lives in the daemon, not the registry, because both halves of it do:
    /// copying broadcast and multicast to everyone, and pointing this machine's
    /// multicast at the tunnel. The second is what a user might want back, so
    /// the switch applies at once rather than at the next connect.
    /// </summary>
    public bool LanDiscovery
    {
        get => _lanDiscovery;
        set
        {
            if (_lanDiscovery == value)
            {
                return;
            }
            _lanDiscovery = value;
            OnPropertyChanged();
            _ = ApplyLanDiscoveryAsync(value);
        }
    }

    /// <summary>
    /// Moves the switch to what the daemon says without telling it back.
    /// </summary>
    private void ShowLanDiscovery(bool enabled)
    {
        if (_lanDiscovery == enabled)
        {
            return;
        }
        _lanDiscovery = enabled;
        OnPropertyChanged(nameof(LanDiscovery));
    }

    /// <summary>
    /// Tells the daemon, and puts the switch back if it would not. A switch
    /// that stays where it was put while nothing changed is the worst outcome.
    /// </summary>
    private async Task ApplyLanDiscoveryAsync(bool enabled)
    {
        try
        {
            await _daemon.SetLanDiscoveryAsync(enabled);
        }
        catch (DaemonException e)
        {
            _lanDiscovery = !enabled;
            OnPropertyChanged(nameof(LanDiscovery));
            Status = ErrorCopy.Describe(e);
        }
    }

    private bool _packetLog;

    /// <summary>
    /// Whether the daemon writes a line for every packet worth seeing.
    ///
    /// It is here rather than on by default because it is for catching one
    /// misbehaving session, not for running all the time. The counters that
    /// summarise into the ordinary log run either way, so leaving this off does
    /// not mean a report arrives with nothing in it.
    /// </summary>
    public bool PacketLog
    {
        get => _packetLog;
        set
        {
            if (_packetLog == value)
            {
                return;
            }
            _packetLog = value;
            OnPropertyChanged();
            _ = ApplyPacketLogAsync(value);
        }
    }

    /// <summary>Moves the switch to what the daemon says without telling it back.</summary>
    private void ShowPacketLog(bool enabled)
    {
        if (_packetLog == enabled)
        {
            return;
        }
        _packetLog = enabled;
        OnPropertyChanged(nameof(PacketLog));
    }

    /// <summary>
    /// Tells the daemon, and shows what it settled on rather than what was
    /// asked. Opening the log can fail, and a switch reading on over a log that
    /// is not being written is worse than one that refused to move.
    /// </summary>
    private async Task ApplyPacketLogAsync(bool enabled)
    {
        try
        {
            ShowPacketLog((await _daemon.SetPacketLogAsync(enabled)).Enabled);
        }
        catch (DaemonException e)
        {
            ShowPacketLog(!enabled);
            Status = ErrorCopy.Describe(e);
        }
    }

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
            _savedNickname = _daemon.State.Nickname ?? "";
            Nickname = _savedNickname;
            ShowLanDiscovery((await _daemon.GetLanDiscoveryAsync()).Enabled);
            ShowPacketLog((await _daemon.GetPacketLogAsync()).Enabled);
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
    /// Saves the name and the address. Switching servers disconnects and
    /// registers again, because a device credential is only good where it was
    /// issued.
    /// </summary>
    /// <returns>Whether the dialog should close.</returns>
    [RelayCommand]
    public async Task<bool> SaveAsync()
    {
        var nickname = (Nickname ?? "").Trim();
        if (nickname.Length == 0)
        {
            Status = "Your name cannot be empty.";
            return false;
        }

        IsBusy = true;
        try
        {
            // The name goes first. Setting the server can disconnect and
            // register again, which would swallow it.
            if (nickname != _savedNickname)
            {
                await _daemon.SetNicknameAsync(nickname);
                _savedNickname = nickname;
            }
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
            ShowLanDiscovery((await _daemon.GetLanDiscoveryAsync()).Enabled);
            ShowPacketLog((await _daemon.GetPacketLogAsync()).Enabled);
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
