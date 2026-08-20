using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Net192168.Client.Ipc;

namespace Net192168.Client.ViewModels;

/// <summary>One group as the list shows it.</summary>
public sealed partial class GroupListItem : ObservableObject
{
    [ObservableProperty]
    public partial string? Name { get; set; }

    [ObservableProperty]
    public partial string? Nickname { get; set; }

    [ObservableProperty]
    public partial bool IsActive { get; set; }

    public string GroupId { get; init; } = "";

    /// <summary>The button says what clicking it will do.</summary>
    public string ActionLabel => IsActive ? "Disconnect" : "Connect";

    partial void OnIsActiveChanged(bool value) => OnPropertyChanged(nameof(ActionLabel));
}

/// <summary>The groups screen.</summary>
public sealed partial class GroupsViewModel : ObservableObject
{
    private readonly Daemon _daemon;

    public GroupsViewModel(Daemon daemon)
    {
        _daemon = daemon;
        _daemon.StateChanged += OnStateChanged;
    }

    public ObservableCollection<GroupListItem> Groups { get; } = [];

    [ObservableProperty]
    public partial bool IsBusy { get; set; }

    [ObservableProperty]
    public partial string? Error { get; set; }

    [ObservableProperty]
    public partial bool IsEmpty { get; set; }

    /// <summary>Reads the group list from the daemon.</summary>
    [RelayCommand]
    public async Task RefreshAsync()
    {
        if (!_daemon.IsAvailable)
        {
            Error = "The background service is not running.";
            return;
        }

        IsBusy = true;
        try
        {
            var groups = await _daemon.GetGroupsAsync();
            var activeGroupId = _daemon.State.GroupId;

            Groups.Clear();
            foreach (var group in groups)
            {
                Groups.Add(new GroupListItem
                {
                    GroupId = group.GroupId,
                    Name = group.Name,
                    Nickname = group.Nickname,
                    IsActive = group.GroupId == activeGroupId,
                });
            }
            IsEmpty = Groups.Count == 0;
            Error = null;
        }
        catch (DaemonException e)
        {
            Error = e.Message;
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>
    /// Connects to a group, or disconnects if it is the one already active.
    /// Connecting to another group while one is up is left to the daemon, which
    /// tears the first one down first.
    /// </summary>
    [RelayCommand]
    public async Task ToggleAsync(GroupListItem? item)
    {
        if (item is null)
        {
            return;
        }

        IsBusy = true;
        try
        {
            if (item.IsActive)
            {
                await _daemon.DisconnectAsync();
            }
            else
            {
                await _daemon.ConnectGroupAsync(item.GroupId);
            }
            Error = null;
        }
        catch (DaemonException e)
        {
            Error = e.Message;
        }
        finally
        {
            IsBusy = false;
        }
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

    [RelayCommand]
    public async Task LeaveAsync(GroupListItem? item)
    {
        if (item is null)
        {
            return;
        }
        try
        {
            await _daemon.LeaveGroupAsync(item.GroupId);
            await RefreshAsync();
        }
        catch (DaemonException e)
        {
            Error = e.Message;
        }
    }

    private void OnStateChanged()
    {
        var activeGroupId = _daemon.State.GroupId;
        foreach (var item in Groups)
        {
            item.IsActive = item.GroupId == activeGroupId;
        }
    }
}
