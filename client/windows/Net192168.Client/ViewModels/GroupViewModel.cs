using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Microsoft.UI.Dispatching;
using Net192168.Client.Ipc;
using Windows.ApplicationModel.DataTransfer;

namespace Net192168.Client.ViewModels;

/// <summary>
/// The create and join screen. Creating asks for a name and a look, then ends on
/// the link. Joining asks for an invite and names what it opens.
/// </summary>
public sealed partial class GroupViewModel : ObservableObject
{
    private readonly Daemon _daemon;
    private readonly bool _creating;

    /// <summary>
    /// Waits out the typing before asking what an invite opens. Typing a code is
    /// eight changes, seven of which open nothing.
    /// </summary>
    private readonly DispatcherQueueTimer? _lookUp;

    /// <summary>Puts the copy button back after its tick.</summary>
    private readonly DispatcherQueueTimer? _copyFeedback;

    public GroupViewModel(Daemon daemon, bool creating)
    {
        _daemon = daemon;
        _creating = creating;

        var queue = DispatcherQueue.GetForCurrentThread();
        _lookUp = queue?.CreateTimer();
        if (_lookUp is not null)
        {
            _lookUp.Interval = TimeSpan.FromMilliseconds(400);
            _lookUp.IsRepeating = false;
            _lookUp.Tick += async (_, _) => await LookUpInviteAsync();
        }

        _copyFeedback = queue?.CreateTimer();
        if (_copyFeedback is not null)
        {
            _copyFeedback.Interval = TimeSpan.FromSeconds(2);
            _copyFeedback.IsRepeating = false;
            _copyFeedback.Tick += (_, _) => JustCopied = false;
        }
    }

    /// <summary>Whether a group is being made. Joining one does not pick its look.</summary>
    public bool IsCreating => _creating;

    public bool IsJoining => !_creating;

    /// <summary>The look a new group is made with. Unused when joining.</summary>
    public GroupLookChoice Appearance { get; } = new();

    public string Title => _creating ? "Create a group" : "Join a group";

    public string SubmitLabel => _creating ? "Create" : "Join";

    public string Hint => _creating
        ? "You will get a link to send people."
        : "Paste the link or code somebody sent you.";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmit))]
    public partial string? GroupName { get; set; }

    /// <summary>The invite being joined with, as pasted. It may be a link.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmit))]
    public partial string? Invite { get; set; }

    partial void OnInviteChanged(string? value)
    {
        // The last answer described the previous text.
        Found = null;
        _lookUp?.Stop();
        if (!string.IsNullOrWhiteSpace(value))
        {
            _lookUp?.Start();
        }
    }

    /// <summary>
    /// What the invite opens. Null before anything is typed, while the answer is
    /// still coming, and for an invite that opens nothing.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(HasFound))]
    [NotifyPropertyChangedFor(nameof(FoundLook))]
    [NotifyPropertyChangedFor(nameof(FoundName))]
    [NotifyPropertyChangedFor(nameof(FoundMembers))]
    public partial InviteResult? Found { get; set; }

    public bool HasFound => Found is not null;

    public GroupLook FoundLook => GroupLooks.For(Found?.GroupIcon, Found?.GroupColor);

    public string FoundName => Found?.GroupName ?? "";

    public string FoundMembers => Found is null ? "" : Members(Found.Members);

    /// <summary>The link to the group that was just made. Null until then.</summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(IsDone))]
    [NotifyPropertyChangedFor(nameof(IsNotDone))]
    [NotifyPropertyChangedFor(nameof(DoneHeadline))]
    public partial string? Link { get; set; }

    /// <summary>True once the group exists and the screen is showing its link.</summary>
    public bool IsDone => Link is not null;

    public bool IsNotDone => !IsDone;

    public string DoneHeadline => $"{(GroupName ?? "").Trim()} is ready";

    /// <summary>True for a moment after the link was put on the clipboard.</summary>
    [ObservableProperty]
    public partial bool JustCopied { get; set; }

    /// <summary>
    /// What went wrong, shown under the form. The screen stays put, since the fix
    /// is usually one character.
    /// </summary>
    [ObservableProperty]
    public partial string? Error { get; set; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmit))]
    public partial bool IsBusy { get; set; }

    /// <summary>
    /// Whether the form is worth submitting. Every field it reads raises a
    /// change for it, so the button follows the typing.
    /// </summary>
    public bool CanSubmit => !IsBusy && (_creating
        ? (GroupName ?? "").Trim().Length > 0
        : (Invite ?? "").Trim().Length > 0);

    /// <summary>Creates or joins.</summary>
    /// <returns>Whether the screen is finished with. Creating is not: it has a
    /// link to hand over first.</returns>
    public async Task<bool> SubmitAsync()
    {
        IsBusy = true;
        Error = null;
        try
        {
            if (_creating)
            {
                var group = await _daemon.CreateGroupAsync(
                    (GroupName ?? "").Trim(), Appearance.Icon.Key, Appearance.Color.Key);
                // A server that did not say where its links live still gives a
                // code, and a code is enough to send.
                Link = group.InviteLink.Length > 0 ? group.InviteLink : group.InviteCode;
                return false;
            }

            await _daemon.JoinGroupAsync((Invite ?? "").Trim());
            return true;
        }
        catch (DaemonException e)
        {
            Error = ErrorCopy.Describe(e, _creating ? ErrorContext.General : ErrorContext.Join);
            return false;
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>Puts the new group's link on the clipboard.</summary>
    [RelayCommand]
    public void CopyLink()
    {
        if (Link is null)
        {
            return;
        }

        var package = new DataPackage();
        package.SetText(Link);
        Clipboard.SetContent(package);

        JustCopied = true;
        _copyFeedback?.Start();
    }

    /// <summary>
    /// Asks what the current invite opens. Failing is quiet: the screen just does
    /// not name a group. Joining is where a bad invite is reported.
    /// </summary>
    private async Task LookUpInviteAsync()
    {
        var asked = (Invite ?? "").Trim();
        if (asked.Length == 0)
        {
            return;
        }

        try
        {
            var found = await _daemon.GetInviteAsync(asked);
            // The text may have moved on while the server answered.
            if (found.Found && asked == (Invite ?? "").Trim())
            {
                Found = found;
            }
        }
        catch (DaemonException e)
        {
            App.Trace($"invite lookup failed: code={e.Code} message={e.Message}");
        }
    }

    private static string Members(int count) => count == 1 ? "1 person" : $"{count} people";
}
