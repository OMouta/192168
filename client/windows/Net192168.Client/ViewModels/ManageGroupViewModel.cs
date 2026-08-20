using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Net192168.Client.Ipc;

namespace Net192168.Client.ViewModels;

/// <summary>
/// The two things an owner can change about a group itself. Who is in it is
/// managed from the rows on the screen this came from, where the people are.
/// </summary>
public sealed partial class ManageGroupViewModel : ObservableObject
{
    private readonly Daemon _daemon;
    private readonly string _groupId;

    public ManageGroupViewModel(Daemon daemon, string groupId, string name)
    {
        _daemon = daemon;
        _groupId = groupId;
        Name = name;
        OriginalName = name;
    }

    [ObservableProperty]
    public partial string Name { get; set; }

    /// <summary>What the group was called on arrival, so Save can tell whether
    /// the name is worth sending.</summary>
    public string OriginalName { get; }

    [ObservableProperty]
    public partial string Password { get; set; } = "";

    [ObservableProperty]
    public partial string? Status { get; set; }

    [ObservableProperty]
    public partial bool IsBusy { get; set; }

    /// <summary>
    /// Applies whichever of the two was changed.
    /// </summary>
    /// <returns>Whether the screen is finished with.</returns>
    [RelayCommand]
    public async Task<bool> SaveAsync()
    {
        var name = (Name ?? "").Trim();
        var password = Password ?? "";

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
            if (password != "")
            {
                await _daemon.SetGroupPasswordAsync(_groupId, password);
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
