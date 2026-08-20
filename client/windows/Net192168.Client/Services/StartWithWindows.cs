using Microsoft.Win32;

namespace Net192168.Client.Services;

/// <summary>
/// Whether the app opens when you sign in.
///
/// The per-user Run key, which is what Task Manager Startup controls, so turning
/// it off there and turning it off here agree.
/// </summary>
public static class StartWithWindows
{
    private const string RunKey = @"Software\Microsoft\Windows\CurrentVersion\Run";
    private const string ValueName = "192168";

    /// <summary>
    /// Passed to the client when Windows starts it, so it comes up in the tray
    /// rather than opening a window.
    /// </summary>
    public const string TrayArgument = "--tray";

    public static bool IsEnabled
    {
        get
        {
            using var key = Registry.CurrentUser.OpenSubKey(RunKey);
            return key?.GetValue(ValueName) is not null;
        }
    }

    /// <summary>Turns the startup entry on or off. Needs no elevation: this is
    /// the current user's own key.</summary>
    public static void Set(bool enabled)
    {
        using var key = Registry.CurrentUser.CreateSubKey(RunKey, writable: true);
        if (key is null)
        {
            return;
        }

        if (!enabled)
        {
            key.DeleteValue(ValueName, throwOnMissingValue: false);
            return;
        }

        var exe = Environment.ProcessPath;
        if (string.IsNullOrEmpty(exe))
        {
            return;
        }
        key.SetValue(ValueName, $"\"{exe}\" {TrayArgument}");
    }
}
