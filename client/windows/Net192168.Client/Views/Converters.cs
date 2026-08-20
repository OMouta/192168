using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Data;
using Microsoft.UI.Xaml.Media;

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
