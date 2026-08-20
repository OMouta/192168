using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.UI.Dispatching;
using Net192168.Client.Ipc;
using Windows.ApplicationModel.DataTransfer;

namespace Net192168.Client.ViewModels;

/// <summary>One saved group.</summary>
public sealed partial class GroupListItem : ObservableObject
{
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(LeavePrompt))]
    public partial string? Name { get; set; }

    [ObservableProperty]
    public partial string? Nickname { get; set; }

    /// <summary>
    /// True while this is the group being brought up. Connecting takes seconds
    /// and involves a server, so the row it was started from is where the wait
    /// has to show.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(ConnectLabel))]
    public partial bool IsConnecting { get; set; }

    /// <summary>
    /// False while something else is in flight. Only one group can be active, so
    /// a second Connect would be turned down by the daemon as busy anyway.
    /// </summary>
    [ObservableProperty]
    public partial bool CanConnect { get; set; } = true;

    /// <summary>
    /// True while this row is asking whether to leave. The question is asked in
    /// the row rather than over the window, which in a window this narrow would
    /// cover the group being left.
    /// </summary>
    [ObservableProperty]
    public partial bool IsConfirmingLeave { get; set; }

    public string GroupId { get; init; } = "";

    public string ConnectLabel => IsConnecting ? "Connecting" : "Connect";

    public string LeavePrompt => $"Leave {Name}? You will need the password to join again.";
}

/// <summary>
/// One connected peer.
///
/// A row that changes in place rather than a new one each time. Latency arrives
/// every few seconds, and rebuilding the list on every update made rows vanish
/// and reappear instead of quietly changing their number.
/// </summary>
public sealed partial class PeerItem(string deviceId) : ObservableObject
{
    /// <summary>Which peer this row is, so an update finds it again.</summary>
    public string DeviceId { get; } = deviceId;

    [ObservableProperty]
    public partial string Nickname { get; set; } = "";

    [ObservableProperty]
    public partial string VirtualIp { get; set; } = "";

    [ObservableProperty]
    public partial string Status { get; set; } = "";

    [ObservableProperty]
    public partial bool IsReachable { get; set; }

    /// <summary>
    /// False for someone who is in the group but not connected. Their row is
    /// dimmed and has no address to copy, because they have not been given one.
    /// </summary>
    [ObservableProperty]
    public partial bool IsHere { get; set; } = true;

    /// <summary>True for a moment after copying, so the button can say so.</summary>
    [ObservableProperty]
    public partial bool JustCopied { get; set; }

    /// <summary>
    /// Puts this peer's address on the clipboard. It is the thing people type
    /// into a game, and reading it off the screen to type by hand is the worst
    /// part of using this.
    /// </summary>
    [RelayCommand]
    private async Task CopyAddressAsync()
    {
        if (string.IsNullOrEmpty(VirtualIp))
        {
            return;
        }

        var package = new DataPackage();
        package.SetText(VirtualIp);
        Clipboard.SetContent(package);

        JustCopied = true;
        await Task.Delay(TimeSpan.FromSeconds(1.5));
        JustCopied = false;
    }
}

/// <summary>
/// The whole window. The app is one screen that changes with the connection, so
/// there is one view model rather than a page each and state split between them.
/// </summary>
public sealed partial class HomeViewModel : ObservableObject
{
    private readonly Daemon _daemon;

    /// <summary>
    /// Takes the tick off the copy button again. Copying an address has no
    /// visible result of its own, and the alternative to a moment of feedback is
    /// a line of text that then sits on the screen forever.
    /// </summary>
    private readonly DispatcherQueueTimer? _copyFeedback;

    public HomeViewModel(Daemon daemon)
    {
        _daemon = daemon;

        _copyFeedback = DispatcherQueue.GetForCurrentThread()?.CreateTimer();
        if (_copyFeedback is not null)
        {
            _copyFeedback.Interval = TimeSpan.FromSeconds(1.5);
            _copyFeedback.IsRepeating = false;
            _copyFeedback.Tick += (_, _) => JustCopied = false;
        }

        _daemon.StateChanged += Update;
        Update();
    }

    public ObservableCollection<GroupListItem> Groups { get; } = [];

    public ObservableCollection<PeerItem> Peers { get; } = [];

    /// <summary>True while a group is up, which is what swaps the screen over.</summary>
    [ObservableProperty]
    public partial bool IsConnected { get; set; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsWorking))]
    [NotifyPropertyChangedFor(nameof(CanAct))]
    public partial bool IsBusy { get; set; }

    /// <summary>
    /// What the daemon is doing. Connect is answered as soon as the attempt
    /// starts rather than when it finishes, so this, and not
    /// <see cref="IsBusy"/>, is what says the app is still waiting.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsWorking))]
    [NotifyPropertyChangedFor(nameof(CanAct))]
    public partial ConnectionState Connection { get; set; }

    /// <summary>True whenever something is in flight, whoever started it.</summary>
    public bool IsWorking =>
        IsBusy || Connection is ConnectionState.Connecting or ConnectionState.Disconnecting;

    /// <summary>Whether the buttons on the screen would do anything if pressed.</summary>
    public bool CanAct => IsReady && !IsWorking;

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

    /// <summary>
    /// What went wrong, if anything. Only failures land here: everything that
    /// worked is already visible in what the screen now says.
    /// </summary>
    [ObservableProperty]
    public partial string? Message { get; set; }

    /// <summary>False when the daemon is not answering, which disables everything.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanAct))]
    public partial bool IsReady { get; set; }

    /// <summary>
    /// True while the nickname is being edited in place. A name is one word and
    /// changing it is common, so it is not worth a screen of its own.
    /// </summary>
    [ObservableProperty]
    public partial bool IsEditingNickname { get; set; }

    /// <summary>What is in the edit box, kept apart so cancelling is possible.</summary>
    [ObservableProperty]
    public partial string? NicknameDraft { get; set; }

    /// <summary>
    /// True for a moment after the address was put on the clipboard, which the
    /// copy button shows as a tick.
    /// </summary>
    [ObservableProperty]
    public partial bool JustCopied { get; set; }

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
            ApplyGroupStates();
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

    /// <summary>
    /// Asks whether to leave, in the row itself.
    ///
    /// The daemon never stores a group password, so getting back in means having
    /// it to hand again. That is worth a question, and the answer names the
    /// group so it is clear which one is going.
    /// </summary>
    [RelayCommand]
    public void StartLeaving(GroupListItem? item)
    {
        foreach (var group in Groups)
        {
            group.IsConfirmingLeave = ReferenceEquals(group, item);
        }
    }

    [RelayCommand]
    public void CancelLeaving(GroupListItem? item)
    {
        if (item is not null)
        {
            item.IsConfirmingLeave = false;
        }
    }

    [RelayCommand]
    public async Task LeaveAsync(GroupListItem? item)
    {
        if (item is null)
        {
            return;
        }
        item.IsConfirmingLeave = false;
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

        JustCopied = true;
        _copyFeedback?.Start();
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
            ApplyGroupStates();
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
        Connection = state.Connection;
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

        UpdatePeers(state.Peers);
        HasPeers = Peers.Count > 0;
        ShowEmptyPeers = IsConnected && !HasPeers;
        ShowEmptyGroups = !IsConnected && !HasGroups;

        ApplyGroupStates();

        if (justBecameReady)
        {
            _ = RefreshAsync();
        }
    }

    /// <summary>
    /// Brings the peer list in line with what the daemon reports, changing the
    /// rows that are already there.
    ///
    /// Emptying the list and filling it again is the obvious way to write this
    /// and the wrong one: a peer's latency changes every few seconds, and every
    /// one of those took every row off the screen and put it back, so the list
    /// flickered and nothing could be clicked reliably.
    /// </summary>
    private void UpdatePeers(IReadOnlyList<PeerView> peers)
    {
        for (var i = Peers.Count - 1; i >= 0; i--)
        {
            if (!peers.Any(p => p.DeviceId == Peers[i].DeviceId))
            {
                Peers.RemoveAt(i);
            }
        }

        foreach (var peer in peers)
        {
            var row = Peers.FirstOrDefault(p => p.DeviceId == peer.DeviceId);
            if (row is null)
            {
                row = new PeerItem(peer.DeviceId);
                Peers.Add(row);
            }

            row.Nickname = peer.Nickname;
            row.VirtualIp = peer.VirtualIp;
            row.Status = Describe(peer);
            row.IsReachable = peer.State == PeerState.Direct;
            row.IsHere = peer.State != PeerState.Offline;
        }
    }

    /// <summary>
    /// Tells each row what it may do. A row cannot see the state of the screen
    /// from inside its template, so it is pushed down to it.
    /// </summary>
    private void ApplyGroupStates()
    {
        OnPropertyChanged(nameof(IsWorking));
        OnPropertyChanged(nameof(CanAct));

        var connecting = Connection == ConnectionState.Connecting ? _groupId : null;

        foreach (var group in Groups)
        {
            group.IsConnecting = connecting is not null && group.GroupId == connecting;
            group.CanConnect = CanAct;

            // Nothing can be answered while the answer would be refused.
            if (!CanAct)
            {
                group.IsConfirmingLeave = false;
            }
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
