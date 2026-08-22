using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.UI.Dispatching;
using Net192168.Client.Ipc;
using Windows.ApplicationModel.DataTransfer;

namespace Net192168.Client.ViewModels;

/// <summary>
/// What an owner can change about a group itself: what it is called, how it is
/// shown, and the invite that lets the next person in. Who is in it is managed
/// from the rows on the screen this came from, where the people are.
/// </summary>
public sealed partial class ManageGroupViewModel : ObservableObject
{
    private readonly Daemon _daemon;
    private readonly string _groupId;

    private readonly DispatcherQueueTimer? _copyFeedback;

    public ManageGroupViewModel(Daemon daemon, string groupId, string name, string? icon, string? color, string? inviteLink)
    {
        _daemon = daemon;
        _groupId = groupId;
        Name = name;
        OriginalName = name;
        Appearance = new GroupLookChoice(icon, color);
        InviteLink = inviteLink ?? "";

        _copyFeedback = DispatcherQueue.GetForCurrentThread()?.CreateTimer();
        if (_copyFeedback is not null)
        {
            _copyFeedback.Interval = TimeSpan.FromSeconds(2);
            _copyFeedback.IsRepeating = false;
            _copyFeedback.Tick += (_, _) => JustCopied = false;
        }
    }

    /// <summary>
    /// The link that lets somebody in. There is one per group and it does not
    /// expire: if it reaches the wrong person, it is replaced rather than
    /// managed.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasInvite))]
    public partial string InviteLink { get; set; }

    public bool HasInvite => InviteLink.Length > 0;

    /// <summary>True for a moment after the link was put on the clipboard.</summary>
    [ObservableProperty]
    public partial bool JustCopied { get; set; }

    /// <summary>Puts the link on the clipboard, which is the whole of sharing it.</summary>
    [RelayCommand]
    private void CopyInvite()
    {
        if (!HasInvite)
        {
            return;
        }

        var package = new DataPackage();
        package.SetText(InviteLink);
        Clipboard.SetContent(package);

        JustCopied = true;
        _copyFeedback?.Start();
    }

    /// <summary>
    /// Draws a new link and retires the one that was given out. Applied at once
    /// rather than on Save: the old link stops working the moment this is
    /// pressed, and a Save that could undo it would be a lie.
    /// </summary>
    [RelayCommand]
    private async Task ResetInviteAsync()
    {
        IsBusy = true;
        try
        {
            var fresh = await _daemon.ResetInviteAsync(_groupId);
            InviteLink = fresh.Link.Length > 0 ? fresh.Link : fresh.Code;
            Status = "The old link no longer works.";
            JustCopied = false;
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

    [ObservableProperty]
    public partial string Name { get; set; }

    /// <summary>What the group was called on arrival, so Save can tell whether
    /// the name is worth sending.</summary>
    public string OriginalName { get; }

    /// <summary>The icon and colour, as the picker offers them.</summary>
    public GroupLookChoice Appearance { get; }

    [ObservableProperty]
    public partial string? Status { get; set; }

    [ObservableProperty]
    public partial bool IsBusy { get; set; }

    /// <summary>
    /// True once Delete has been pressed and the question is on screen.
    ///
    /// Asked in place rather than over the window: a dialog in a window this
    /// narrow covers the name of the thing being deleted, which is the one fact
    /// worth checking before agreeing.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsNotConfirmingDelete))]
    public partial bool IsConfirmingDelete { get; set; }

    public bool IsNotConfirmingDelete => !IsConfirmingDelete;

    /// <summary>What deleting takes with it, named so it cannot be misread.</summary>
    public string DeletePrompt => $"Delete {OriginalName}? It goes for everyone in it, and cannot be undone.";

    [RelayCommand]
    private void AskToDelete() => IsConfirmingDelete = true;

    [RelayCommand]
    private void KeepGroup() => IsConfirmingDelete = false;

    /// <summary>
    /// Removes the group for everybody. The daemon disconnects first, since
    /// this device is the one holding an adapter for a group about to stop
    /// existing.
    /// </summary>
    /// <returns>Whether the screen is finished with.</returns>
    [RelayCommand]
    public async Task<bool> DeleteAsync()
    {
        IsBusy = true;
        try
        {
            await _daemon.DeleteGroupAsync(_groupId);
            return true;
        }
        catch (DaemonException e)
        {
            Status = ErrorCopy.Describe(e);
            IsConfirmingDelete = false;
            return false;
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>
    /// Applies whichever of these was changed.
    /// </summary>
    /// <returns>Whether the screen is finished with.</returns>
    [RelayCommand]
    public async Task<bool> SaveAsync()
    {
        var name = (Name ?? "").Trim();
        if (name == "")
        {
            Status = "A group needs a name.";
            return false;
        }

        IsBusy = true;
        try
        {
            if (name != OriginalName)
            {
                await _daemon.RenameGroupAsync(_groupId, name);
            }
            if (Appearance.Changed)
            {
                await _daemon.SetGroupAppearanceAsync(_groupId, Appearance.Icon.Key, Appearance.Color.Key);
            }
            return true;
        }
        catch (DaemonException e)
        {
            // Staying keeps the reason next to what caused it, and keeps the
            // typing.
            Status = ErrorCopy.Describe(e);
            return false;
        }
        finally
        {
            IsBusy = false;
        }
    }
}
