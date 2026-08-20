using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Net192168.Client.Ipc;
using Windows.ApplicationModel.DataTransfer;

namespace Net192168.Client.ViewModels;

/// <summary>One saved group.</summary>
public sealed partial class GroupListItem : ObservableObject
{
    [ObservableProperty]
    public partial string? Name { get; set; }

    [ObservableProperty]
    public partial string? Nickname { get; set; }

    public string GroupId { get; init; } = "";
}

/// <summary>One connected peer.</summary>
public sealed record PeerItem(string Nickname, string VirtualIp, string Status, bool IsReachable);

/// <summary>
/// The whole window. The app is one screen that changes with the connection, so
/// there is one view model rather than a page each and state split between them.
/// </summary>
public sealed partial class HomeViewModel : ObservableObject
{
    private readonly Daemon _daemon;

    public HomeViewModel(Daemon daemon)
    {
        _daemon = daemon;
        _daemon.StateChanged += Update;
        Update();
    }

    public ObservableCollection<GroupListItem> Groups { get; } = [];

    public ObservableCollection<PeerItem> Peers { get; } = [];

    /// <summary>True while a group is up, which is what swaps the screen over.</summary>
    [ObservableProperty]
    public partial bool IsConnected { get; set; }

    [ObservableProperty]
    public partial bool IsBusy { get; set; }

    /// <summary>The active group name, shown above the address.</summary>
    [ObservableProperty]
    public partial string? GroupLabel { get; set; }

    /// <summary>Names whichever list is on screen.</summary>
    [ObservableProperty]
    public partial string? ListLabel { get; set; }

    [ObservableProperty]
    public partial string? Status { get; set; }

    [ObservableProperty]
    public partial string? Nickname { get; set; }

    [ObservableProperty]
    public partial string? VirtualIp { get; set; }

    [ObservableProperty]
    public partial bool HasGroups { get; set; }

    [ObservableProperty]
    public partial bool HasPeers { get; set; }

    /// <summary>The empty states are only right on the matching screen.</summary>
    [ObservableProperty]
    public partial bool ShowEmptyPeers { get; set; }

    [ObservableProperty]
    public partial bool ShowEmptyGroups { get; set; }

    [ObservableProperty]
    public partial string? Message { get; set; }

    /// <summary>False when the daemon is not answering, which disables everything.</summary>
    [ObservableProperty]
    public partial bool IsReady { get; set; }

    /// <summary>
    /// True while the nickname is being edited in place. A name is one word and
    /// changing it is common, so it is not worth a dialog.
    /// </summary>
    [ObservableProperty]
    public partial bool IsEditingNickname { get; set; }

    /// <summary>What is in the edit box, kept apart so cancelling is possible.</summary>
    [ObservableProperty]
    public partial string? NicknameDraft { get; set; }

    private string _groupId = "";

    [RelayCommand]
    public async Task RefreshAsync()
    {
        if (!_daemon.IsAvailable)
        {
            return;
        }

        IsBusy = true;
        try
        {
            var groups = await _daemon.GetGroupsAsync();
            Groups.Clear();
            foreach (var group in groups)
            {
                Groups.Add(new GroupListItem
                {
                    GroupId = group.GroupId,
                    Name = group.Name,
                    Nickname = group.Nickname,
                });
            }
            HasGroups = Groups.Count > 0;
            ListLabel = IsConnected ? "On this network" : "Your groups";
            ShowEmptyGroups = !IsConnected && !HasGroups;
            Message = null;
        }
        catch (DaemonException e)
        {
            Message = ErrorCopy.Describe(e);
        }
        finally
        {
            IsBusy = false;
        }
    }

    [RelayCommand]
    public async Task ConnectAsync(GroupListItem? item)
    {
        if (item is null)
        {
            return;
        }
        await Run(() => _daemon.ConnectGroupAsync(item.GroupId));
    }

    [RelayCommand]
    public Task DisconnectAsync() => Run(_daemon.DisconnectAsync);

    [RelayCommand]
    public async Task LeaveAsync(GroupListItem? item)
    {
        if (item is null)
        {
            return;
        }
        await Run(() => _daemon.LeaveGroupAsync(item.GroupId));
        await RefreshAsync();
    }

    /// <summary>Puts the address on the clipboard, which is where it is going anyway.</summary>
    [RelayCommand]
    public void CopyAddress()
    {
        if (string.IsNullOrEmpty(VirtualIp))
        {
            return;
        }

        var package = new DataPackage();
        package.SetText(VirtualIp);
        Clipboard.SetContent(package);
        Message = $"Copied {VirtualIp}";
    }

    [RelayCommand]
    public void StartEditingNickname()
    {
        NicknameDraft = Nickname;
        IsEditingNickname = true;
    }

    [RelayCommand]
    public void CancelEditingNickname() => IsEditingNickname = false;

    /// <summary>Saves the edited nickname, unless it did not change.</summary>
    [RelayCommand]
    public async Task CommitNicknameAsync()
    {
        if (!IsEditingNickname)
        {
            return;
        }
        IsEditingNickname = false;

        var name = (NicknameDraft ?? "").Trim();
        if (name.Length == 0 || name == Nickname || _groupId.Length == 0)
        {
            return;
        }

        await Run(() => _daemon.SetNicknameAsync(_groupId, name));
    }

    public async Task CreateAsync(string name, string password, string nickname)
    {
        await _daemon.CreateGroupAsync(name, password, nickname);
        await RefreshAsync();
    }

    public async Task JoinAsync(string group, string password, string nickname)
    {
        await _daemon.JoinGroupAsync(group, password, nickname);
        await RefreshAsync();
    }

    private async Task Run(Func<Task> action)
    {
        IsBusy = true;
        try
        {
            await action();
            Message = null;
        }
        catch (DaemonException e)
        {
            Message = ErrorCopy.Describe(e);
        }
        finally
        {
            IsBusy = false;
        }
    }

    private void Update()
    {
        var state = _daemon.State;

        // The daemon connects a moment after the window opens, so the first
        // attempt to load groups happens before there is anything to ask. This
        // is where that gets picked up.
        var justBecameReady = !IsReady && _daemon.IsAvailable;
        IsReady = _daemon.IsAvailable;
        IsConnected = state.Connection == ConnectionState.Connected;
        _groupId = state.GroupId ?? "";
        Nickname = state.Nickname;
        VirtualIp = state.VirtualIp;
        GroupLabel = IsConnected ? state.GroupName : "No group connected";
        ListLabel = IsConnected ? "On this network" : "Your groups";
        Status = Describe(state);

        if (!IsConnected)
        {
            IsEditingNickname = false;
        }

        if (!string.IsNullOrEmpty(state.Message))
        {
            Message = state.Message;
        }

        Peers.Clear();
        foreach (var peer in state.Peers)
        {
            Peers.Add(new PeerItem(
                peer.Nickname,
                peer.VirtualIp,
                Describe(peer),
                peer.State == PeerState.Direct));
        }
        HasPeers = Peers.Count > 0;
        ShowEmptyPeers = IsConnected && !HasPeers;
        ShowEmptyGroups = !IsConnected && !HasGroups;

        if (justBecameReady)
        {
            _ = RefreshAsync();
        }
    }

    private string Describe(DaemonState state) => state.Connection switch
    {
        ConnectionState.Connected => state.Peers.Count switch
        {
            0 => "Connected",
            1 => "1 other online",
            var n => $"{n} others online",
        },
        ConnectionState.Connecting => "Connecting",
        ConnectionState.Disconnecting => "Disconnecting",
        _ => _daemon.IsAvailable ? "Pick a group to join" : "Background service is not running",
    };

    /// <summary>
    /// What a player is told about one peer. It says whether they can be
    /// reached, not how the connection was made.
    /// </summary>
    private static string Describe(PeerView peer) => peer.State switch
    {
        PeerState.Direct when peer.LatencyMs is int ms => $"{ms} ms",
        PeerState.Direct => "Direct",
        PeerState.Indirect => "Relayed",
        PeerState.Connecting => "Connecting",
        PeerState.Failed => "Unreachable",
        _ => "Offline",
    };
}
