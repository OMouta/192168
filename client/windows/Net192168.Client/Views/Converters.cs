using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Data;
using Microsoft.UI.Xaml.Media;
using Net192168.Client.Ipc;

namespace Net192168.Client.Views;

/// <summary>Shows an element only when a flag is set.</summary>
public sealed class BooleanToVisibility : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
        => value is true ? Visibility.Visible : Visibility.Collapsed;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => value is Visibility.Visible;
}

/// <summary>Shows an element only when a flag is clear.</summary>
public sealed class BooleanToCollapsed : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
        => value is true ? Visibility.Collapsed : Visibility.Visible;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => value is not Visibility.Visible;
}

/// <summary>
/// True when there is something to say. Used to open a message bar only when a
/// message exists, so an empty one never takes up space.
/// </summary>
public sealed class NotNullToBoolean : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
        => value is string text ? !string.IsNullOrWhiteSpace(text) : value is not null;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => throw new NotSupportedException();
}

/// <summary>
/// The dot beside a name. Green when traffic can actually flow, grey when it
/// cannot, which is the only distinction a player cares about.
/// </summary>
public sealed class ConnectionBrush : IValueConverter
{
    private static readonly SolidColorBrush Live = new(ColorHelper.FromArgb(0xFF, 0x4D, 0xD0, 0xA0));
    private static readonly SolidColorBrush Idle = new(ColorHelper.FromArgb(0xFF, 0x4A, 0x50, 0x5E));

    public object Convert(object value, Type targetType, object parameter, string language)
        => value is true ? Live : Idle;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => throw new NotSupportedException();
}

/// <summary>Shows an element only when there is text to put in it.</summary>
public sealed class TextToVisibility : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
        => value is string text && !string.IsNullOrWhiteSpace(text) ? Visibility.Visible : Visibility.Collapsed;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => throw new NotSupportedException();
}

/// <summary>Inverts a flag, for enabling something while nothing is in flight.</summary>
public sealed class NotBoolean : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
        => value is not true;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => value is not true;
}

/// <summary>
/// Swaps the copy icon for a tick just after copying, so the button answers for
/// itself rather than needing a line of text somewhere that has to be cleared.
/// </summary>
public sealed class CopyGlyph : IValueConverter
{
    // The same two the header uses: a tick just after copying, a pair of pages
    // the rest of the time.
    private const string Copied = "\uE73E";
    private const string Copy = "\uE8C8";

    public object Convert(object value, Type targetType, object parameter, string language)
        => value is true ? Copied : Copy;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => throw new NotSupportedException();
}

/// <summary>
/// Fades a row for someone who is in the group but not connected. Dimming says
/// "not here" without a second column of words saying it.
/// </summary>
public sealed class PresenceOpacity : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
        => value is true ? 1.0 : 0.45;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => throw new NotSupportedException();
}

/// <summary>
/// An update the server has moved past reads as a warning. An ordinary one is a
/// note, which is what people expect and mostly ignore.
/// </summary>
public sealed class UpdateSeverity : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
        => value is true ? InfoBarSeverity.Warning : InfoBarSeverity.Informational;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => throw new NotSupportedException();
}

/// <summary>
/// Colours the message bar by what the message is. A connection that came up
/// with one feature missing is not the same thing as one that failed, and a red
/// bar over a working group reads as the latter.
/// </summary>
public sealed class MessageLevel : IValueConverter
{
    public object Convert(object value, Type targetType, object parameter, string language)
        => value is MessageSeverity.Warning ? InfoBarSeverity.Warning : InfoBarSeverity.Error;

    public object ConvertBack(object value, Type targetType, object parameter, string language)
        => throw new NotSupportedException();
}
