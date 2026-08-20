using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Data;

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
