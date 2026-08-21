using CommunityToolkit.Mvvm.ComponentModel;
using Net192168.Client.Ipc;

namespace Net192168.Client.ViewModels;

/// <summary>
/// The create and join screen. Both ask for the same three things, so they are
/// one screen with different words rather than two forms to keep in step.
/// </summary>
public sealed partial class GroupViewModel : ObservableObject
{
    private readonly Daemon _daemon;
    private readonly bool _creating;

    public GroupViewModel(Daemon daemon, bool creating)
    {
        _daemon = daemon;
        _creating = creating;
    }

    /// <summary>Whether a group is being made. Joining one does not pick its look.</summary>
    public bool IsCreating => _creating;

    /// <summary>The look a new group is made with. Unused when joining.</summary>
    public GroupLookChoice Appearance { get; } = new();

    /// <summary>Names the field and the picker under it, so it says both when there are both.</summary>
    public string NameLabel => _creating ? "Group name & icon" : "Group name";

    public string Title => _creating ? "Create a group" : "Join a group";

    public string SubmitLabel => _creating ? "Create" : "Join";

    public string Hint => _creating
        ? "Share the name and password with the people you want in the group."
        : "Ask whoever made the group for its name and password.";

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmit))]
    public partial string? GroupName { get; set; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmit))]
    public partial string? Password { get; set; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(CanSubmit))]
    public partial string? Nickname { get; set; }

    /// <summary>
    /// What went wrong, shown under the form. The screen stays put when a join
    /// fails, because the fix is usually one character of the password and
    /// re-typing all three would be a punishment.
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
    public bool CanSubmit =>
        !IsBusy
        && (GroupName ?? "").Trim().Length > 0
        && (Password ?? "").Length > 0
        && (Nickname ?? "").Trim().Length > 0;

    /// <summary>
    /// Creates or joins, and reports whether the screen is finished with.
    /// </summary>
    public async Task<bool> SubmitAsync()
    {
        IsBusy = true;
        Error = null;
        try
        {
            var name = (GroupName ?? "").Trim();
            var nickname = (Nickname ?? "").Trim();
            if (_creating)
            {
                await _daemon.CreateGroupAsync(
                    name, Password ?? "", nickname, Appearance.Icon.Key, Appearance.Color.Key);
            }
            else
            {
                await _daemon.JoinGroupAsync(name, Password ?? "", nickname);
            }
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
}
