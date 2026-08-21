using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

/// <summary>
/// The icons and colours a group can be given, one row of each.
///
/// A control rather than the same markup on two screens: a group is given a
/// look when it is made and again whenever its owner changes it, and two copies
/// of a picker drift into two different pickers.
/// </summary>
public sealed partial class GroupLookPicker : UserControl
{
    public GroupLookPicker() => InitializeComponent();

    /// <summary>
    /// What is being picked. The screen owns it and reads the answer back off
    /// it, so nothing has to be handed back through here.
    /// </summary>
    public GroupLookChoice? Choice
    {
        get => (GroupLookChoice?)GetValue(ChoiceProperty);
        set => SetValue(ChoiceProperty, value);
    }

    public static readonly DependencyProperty ChoiceProperty = DependencyProperty.Register(
        nameof(Choice),
        typeof(GroupLookChoice),
        typeof(GroupLookPicker),
        // The screen it sits on builds its view model while navigating, so the
        // choice arrives after this control is up and the rows below have to be
        // told to read it again.
        new PropertyMetadata(null, (control, _) => ((GroupLookPicker)control).Bindings.Update()));
}
