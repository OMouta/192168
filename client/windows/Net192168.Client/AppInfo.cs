using System.Reflection;

namespace Net192168.Client;

/// <summary>
/// What the About screen says about this build.
///
/// The version is read from the assembly rather than written here, so the
/// project file stays the one place it is set.
/// </summary>
public static class AppInfo
{
    public static string Version { get; } = ReadVersion();

    private static string ReadVersion()
    {
        var informational = typeof(AppInfo).Assembly
            .GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion;

        if (string.IsNullOrWhiteSpace(informational))
        {
            return typeof(AppInfo).Assembly.GetName().Version?.ToString(3) ?? "unknown";
        }

        // A build from a git checkout gets the commit appended as "+<sha>",
        // which is noise on a screen meant to answer "which version is this".
        var commit = informational.IndexOf('+');
        return commit < 0 ? informational : informational[..commit];
    }
}
