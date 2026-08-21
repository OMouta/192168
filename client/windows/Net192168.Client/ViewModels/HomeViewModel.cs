using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.UI.Dispatching;
using Net192168.Client.Ipc;
using Net192168.Client.Services;
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

    /// <summary>The keys the owner picked. <see cref="GroupLooks"/> says what they look like.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Look))]
    public partial string? Icon { get; set; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Look))]
    public partial string? Color { get; set; }

    /// <summary>How the row draws the group, before its name has been read.</summary>
    public GroupLook Look => GroupLooks.For(Icon, Color);

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

    /// <summary>Whether this device runs the group. It decides whether the row
    /// says so and offers a way into its settings.</summary>
    [ObservableProperty]
    public partial bool IsOwner { get; set; }

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
public sealed partial class PeerItem(string deviceId, HomeViewModel owner) : ObservableObject
{
    /// <summary>Which peer this row is, so an update finds it again.</summary>
    public string DeviceId { get; } = deviceId;

    /// <summary>
    /// The screen this row is on. A row cannot reach the daemon by itself, and
    /// what its menu offers depends on whether this device runs the group.
    /// </summary>
    public HomeViewModel Home { get; } = owner;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(OwnerPrompt))]
    public partial string Nickname { get; set; } = "";

    [ObservableProperty]
    public partial string VirtualIp { get; set; } = "";

    [ObservableProperty]
    public partial string Status { get; set; } = "";

    [ObservableProperty]
    public partial bool IsReachable { get; set; }

    /// <summary>
    /// False for someone who is in the group but not connected. Their row is
    /// dimmed. The address is still theirs and still worth copying, since it is
    /// what you type in next time they host.
    /// </summary>
    [ObservableProperty]
    public partial bool IsHere { get; set; } = true;

    /// <summary>Whether this row may be removed or promoted, which only the
    /// group's owner may do, and never to themselves.</summary>
    public bool CanManage => Home.IsOwner;

    /// <summary>Called when the group changes hands, so the menu is redrawn.</summary>
    public void OwnerChanged() => OnPropertyChanged(nameof(CanManage));

    /// <summary>Takes this person out of the group. The owner's alone.</summary>
    [RelayCommand]
    private Task RemoveAsync() => Home.RemoveMemberAsync(this);

    /// <summary>Whoever runs the group, marked in the list so people know who to
    /// ask, and so the owner can see the mark move when they hand it over.</summary>
    [ObservableProperty]
    public partial bool IsOwner { get; set; }

    /// <summary>
    /// True while this row is asking whether to hand the group over. Handing it
    /// over cannot be undone alone, so it is worth one question.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsNotConfirmingOwner))]
    public partial bool IsConfirmingOwner { get; set; }

    public bool IsNotConfirmingOwner => !IsConfirmingOwner;

    public string OwnerPrompt => $"Make {Nickname} the owner? You stop being it.";

    /// <summary>Asks first. Handing the group over is not undone without the
    /// other person agreeing to hand it back.</summary>
    [RelayCommand]
    private void AskToMakeOwner() => IsConfirmingOwner = true;

    [RelayCommand]
    private void KeepOwnership() => IsConfirmingOwner = false;

    /// <summary>Hands the group over to this person, and stops being its owner.</summary>
    [RelayCommand]
    private async Task MakeOwnerAsync()
    {
        await Home.TransferOwnershipAsync(this);
        IsConfirmingOwner = false;
    }

    /// <summary>
    /// Puts this peer's address on the clipboard. It is the thing people type
    /// into a game, and reading it off the screen to type by hand is the worst
    /// part of using this.
    /// </summary>
    [RelayCommand]
    private void CopyAddress()
    {
        if (string.IsNullOrEmpty(VirtualIp))
        {
            return;
        }

        var package = new DataPackage();
        package.SetText(VirtualIp);
        Clipboard.SetContent(package);
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

        _daemon.StateChanged += ShowState;
        ShowState();

        // The check runs once when the app opens and may land after this is
        // built, so the banner appears when the answer does rather than only on
        // the next launch.
        Updates.Changed += ShowUpdate;
        ShowUpdate();
    }

    private void ShowUpdate() => HasUpdate = Updates.Available is not null;

    public ObservableCollection<GroupListItem> Groups { get; } = [];

    public ObservableCollection<PeerItem> Peers { get; } = [];

    /// <summary>
    /// Whether the banner is up. This screen's own state, not the update's.
    /// Closing it puts a version out of the way until the next launch, and
    /// About still offers it.
    /// </summary>
    [ObservableProperty]
    public partial bool HasUpdate { get; set; }

    /// <summary>The update, shared with About.</summary>
    public UpdateViewModel Update => UpdateViewModel.Current;

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

    /// <summary>
    /// Whether this device runs the connected group. It decides what the screen
    /// offers and nothing more: the server is what refuses the calls.
    /// </summary>
    [ObservableProperty]
    public partial bool IsOwner { get; set; }

    /// <summary>The active group name, shown above the address.</summary>
    [ObservableProperty]
    public partial string? GroupLabel { get; set; }

    /// <summary>The active group's look, the same one its row in the list wore.</summary>
    [ObservableProperty]
    public partial GroupLook ConnectedLook { get; set; } = GroupLooks.For(null, null);

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

    /// <summary>The connected group, for the screens reached from this one.</summary>
    public string ConnectedGroupId => _groupId;

    /// <summary>The connected group's row, which its settings screen starts from.</summary>
    public GroupListItem? ConnectedGroup => Groups.FirstOrDefault(g => g.GroupId == _groupId);

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
                    Icon = group.Icon,
                    Color = group.Color,
                    Nickname = group.Nickname,
                    IsOwner = group.IsOwner,
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

    private void ShowState()
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
        SetOwner(state.IsOwner);
        Nickname = state.Nickname;
        VirtualIp = state.VirtualIp;
        GroupLabel = IsConnected ? state.GroupName : "No group connected";
        ConnectedLook = GroupLooks.For(state.GroupIcon, state.GroupColor);
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
    /// Records who runs the group, and tells the rows: what their menu offers
    /// depends on it, and they cannot see it change from inside themselves.
    /// </summary>
    private void SetOwner(bool owner)
    {
        if (IsOwner == owner)
        {
            return;
        }
        IsOwner = owner;
        foreach (var row in Peers)
        {
            row.OwnerChanged();
        }
    }

    /// <summary>
    /// Leaves the connected group. Disconnecting is part of it, and is the
    /// daemon's job rather than two calls from here.
    /// </summary>
    public async Task LeaveConnectedGroupAsync()
    {
        var groupId = _groupId;
        if (groupId == "")
        {
            return;
        }
        await Run(() => _daemon.LeaveGroupAsync(groupId));
        await RefreshAsync();
    }

    /// <summary>Takes someone out of the group, for good.</summary>
    public async Task RemoveMemberAsync(PeerItem peer)
    {
        await Run(() => _daemon.RemoveMemberAsync(_groupId, peer.DeviceId));
    }

    /// <summary>Hands the group over. This device stops being its owner.</summary>
    public async Task TransferOwnershipAsync(PeerItem peer)
    {
        await Run(() => _daemon.TransferOwnershipAsync(_groupId, peer.DeviceId));
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
                row = new PeerItem(peer.DeviceId, this);
                Peers.Add(row);
            }

            row.Nickname = peer.Nickname;
            row.VirtualIp = peer.VirtualIp;
            row.Status = Describe(peer);
            row.IsReachable = peer.State is PeerState.Direct or PeerState.Indirect;
            row.IsHere = peer.State != PeerState.Offline;
            row.IsOwner = peer.IsOwner;
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
    /// What a player is told about one peer: whether they can be reached, and
    /// how quickly. The one detail of how worth saying is that somebody else is
    /// carrying the link, because it explains the latency and it explains why
    /// the link goes when that person does.
    /// </summary>
    private static string Describe(PeerView peer) => peer.State switch
    {
        PeerState.Direct when peer.LatencyMs is int ms => $"{ms} ms",
        PeerState.Direct => "Direct",
        PeerState.Indirect when peer.LatencyMs is int ms => $"{ms} ms, relayed",
        PeerState.Indirect => "Relayed",
        PeerState.Connecting => "Connecting",
        PeerState.Failed => "Unreachable",
        _ => "Offline",
    };
}
