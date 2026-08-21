using CommunityToolkit.Mvvm.ComponentModel;
using Microsoft.UI;
using Microsoft.UI.Xaml.Media;

namespace Net192168.Client.ViewModels;

/// <summary>One icon a group can be given, and the glyph it is drawn with.</summary>
public sealed record IconChoice(string Key, string Glyph);

/// <summary>One colour a group can be given.</summary>
public sealed record ColorChoice(string Key, Brush Brush);

/// <summary>
/// How a group is drawn: a glyph in a colour, on a tint of the same colour.
/// </summary>
public sealed record GroupLook(string Glyph, Brush Foreground, Brush Background);

/// <summary>
/// A look being picked. Held by the screen that makes a group and by the one
/// that changes it, so the picker itself is written once.
/// </summary>
public sealed partial class GroupLookChoice : ObservableObject
{
    /// <summary>The keys arrived with, to tell whether the choice is worth sending.</summary>
    private readonly string _icon;
    private readonly string _color;

    public GroupLookChoice(string? icon = null, string? color = null)
    {
        _icon = icon ?? "";
        _color = color ?? "";

        // Something is always picked: a group has a look whether or not anybody
        // chose it.
        Icon = GroupLooks.Icon(icon);
        Color = GroupLooks.Color(color);
    }

    public IReadOnlyList<IconChoice> Icons => GroupLooks.Icons;

    public IReadOnlyList<ColorChoice> Colors => GroupLooks.Colors;

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Look))]
    public partial IconChoice Icon { get; set; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Look))]
    public partial ColorChoice Color { get; set; }

    /// <summary>What the two come to: the group as a list row will draw it.</summary>
    public GroupLook Look => GroupLooks.For(Icon.Key, Color.Key);

    /// <summary>Whether this is worth sending.</summary>
    public bool Changed => Icon.Key != _icon || Color.Key != _color;
}

/// <summary>
/// What a group's icon and colour keys mean. The keys travel between machines;
/// this is the only place that says what they look like, and one it has never
/// heard of falls back to the default.
/// </summary>
public static class GroupLooks
{
    /// <summary>The icons on offer, few enough to fit one row of the picker.</summary>
    public static IReadOnlyList<IconChoice> Icons { get; } =
    [
        new("game", "\uE7FC"),
        new("people", "\uE716"),
        new("star", "\uE735"),
        new("heart", "\uEB51"),
        new("flag", "\uE7C1"),
        new("globe", "\uE774"),
        new("home", "\uE80F"),
        new("bolt", "\uE945"),
    ];

    /// <summary>The colours on offer, picked to hold up against the dark window.</summary>
    public static IReadOnlyList<ColorChoice> Colors { get; } =
    [
        new("blue", Fill(0x5B, 0x9C, 0xFF)),
        new("green", Fill(0x4D, 0xD0, 0xA0)),
        new("purple", Fill(0xA7, 0x8B, 0xFA)),
        new("pink", Fill(0xF4, 0x72, 0xB6)),
        new("red", Fill(0xF8, 0x71, 0x71)),
        new("orange", Fill(0xFB, 0x92, 0x3C)),
        new("yellow", Fill(0xF2, 0xC1, 0x4E)),
        new("grey", Fill(0x94, 0xA3, 0xB8)),
    ];

    /// <summary>The look for a pair of keys. Missing or unknown gets the first of each.</summary>
    public static GroupLook For(string? icon, string? color)
    {
        var glyph = Icon(icon).Glyph;
        var brush = Color(color).Brush;
        return new GroupLook(glyph, brush, Tint(brush));
    }

    public static IconChoice Icon(string? key)
        => Icons.FirstOrDefault(i => i.Key == key) ?? Icons[0];

    public static ColorChoice Color(string? key)
        => Colors.FirstOrDefault(c => c.Key == key) ?? Colors[0];

    private static SolidColorBrush Fill(byte r, byte g, byte b)
        => new(ColorHelper.FromArgb(0xFF, r, g, b));

    /// <summary>One tint brush per colour, since the list rebuilds its rows.</summary>
    private static readonly Dictionary<Brush, Brush> Tints = [];

    private static Brush Tint(Brush brush)
    {
        if (Tints.TryGetValue(brush, out var tint))
        {
            return tint;
        }

        var color = ((SolidColorBrush)brush).Color;
        tint = new SolidColorBrush(ColorHelper.FromArgb(0x2E, color.R, color.G, color.B));
        Tints[brush] = tint;
        return tint;
    }
}
