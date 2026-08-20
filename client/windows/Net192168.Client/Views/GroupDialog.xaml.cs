using Microsoft.UI.Xaml.Controls;

namespace Net192168.Client.Views;

public enum GroupDialogMode
{
    Create,
    Join,
}

/// <summary>
/// Creating and joining ask for the same three things, so they are one dialog
/// with different words rather than two forms to keep in step.
/// </summary>
public sealed partial class GroupDialog : ContentDialog
{
    public GroupDialog(GroupDialogMode mode)
    {
        InitializeComponent();

        var creating = mode == GroupDialogMode.Create;
        Title = creating ? "Create a group" : "Join a group";
        PrimaryButtonText = creating ? "Create" : "Join";
        HintText.Text = creating
            ? "Share the name and password with the people you want in the group."
            : "Ask whoever made the group for its name and password.";
    }

    public string GroupName => NameBox.Text.Trim();

    public string Password => PasswordInput.Password;

    public string Nickname => NicknameBox.Text.Trim();

    private void OnChanged(object sender, object args)
    {
        IsPrimaryButtonEnabled = GroupName.Length > 0 && Password.Length > 0 && Nickname.Length > 0;
    }
}
