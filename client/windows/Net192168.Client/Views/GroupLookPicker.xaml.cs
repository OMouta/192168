using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Net192168.Client.ViewModels;

namespace Net192168.Client.Views;

/// <summary>
/// The icons and colours a group can be given, one row of each. A control
/// because two screens offer it: making a group and changing one.
/// </summary>
public sealed partial class GroupLookPicker : UserControl
{
    public GroupLookPicker() => InitializeComponent();

    /// <summary>What is being picked. The screen owns it and reads the answer off it.</summary>
    public GroupLookChoice? Choice
    {
        get => (GroupLookChoice?)GetValue(ChoiceProperty);
        set => SetValue(ChoiceProperty, value);
    }

    public static readonly DependencyProperty ChoiceProperty = DependencyProperty.Register(
        nameof(Choice),
        typeof(GroupLookChoice),
        typeof(GroupLookPicker),
        // The choice arrives after this control is up, so the rows have to be
        // told to read it again.
        new PropertyMetadata(null, (control, _) => ((GroupLookPicker)control).Bindings.Update()));
}
