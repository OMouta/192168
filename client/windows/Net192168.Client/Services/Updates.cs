using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text.Json;
using Microsoft.Win32;

namespace Net192168.Client.Services;

/// <summary>The setup program attached to a release.</summary>
public sealed record Setup(string Name, string Url, long Size);

/// <summary>A release newer than this one, and the installer that applies it.</summary>
public sealed record Release(string Version, string Url, Setup? Installer);

/// <summary>Where an install has got to.</summary>
public enum UpdateStage
{
    Idle,
    Downloading,

    /// <summary>Handed to Windows, which is showing the consent prompt.</summary>
    Starting,
    Failed,
}

/// <summary>
/// Whether there is a newer version, and installing it.
///
/// Installing means downloading the release's installer and running it. That
/// installer already stops the service, replaces both binaries, registers it
/// again and reopens the app, so nothing here has to know how any of it works.
///
/// A check that fails says nothing. No network, a rate limit, a rewritten API:
/// none of that is the user's problem, and an update checker that complains is
/// worse than one that stays quiet. An install that fails does say so, because
/// somebody pressed a button and is waiting.
/// </summary>
public static class Updates
{
    private const string LatestRelease = "https://api.github.com/repos/OMouta/192168/releases/latest";

    /// <summary>Offered when an install fails, and when a release has no URL.</summary>
    public const string ReleasesPage = "https://github.com/OMouta/192168/releases";

    private const string SettingsKey = @"Software\192168";
    private const string EnabledValue = "CheckForUpdates";

    private static readonly HttpClient Http = CreateClient(TimeSpan.FromSeconds(10));

    /// <summary>
    /// A second client because Timeout covers reading the body and not just
    /// getting the headers, so ten seconds would abandon every download.
    /// </summary>
    private static readonly HttpClient Downloads = CreateClient(TimeSpan.FromMinutes(15));

    /// <summary>
    /// Whether to look at all.
    ///
    /// On unless turned off. It is the only thing this app sends anywhere that
    /// is not the coordination server, so it is worth being able to say no to,
    /// and worth it being one switch rather than a page of explanation.
    /// </summary>
    public static bool Enabled
    {
        get
        {
            using var key = Registry.CurrentUser.OpenSubKey(SettingsKey);
            return key?.GetValue(EnabledValue) is not int off || off != 0;
        }
        set
        {
            using var key = Registry.CurrentUser.CreateSubKey(SettingsKey, writable: true);
            key?.SetValue(EnabledValue, value ? 1 : 0, RegistryValueKind.DWord);
        }
    }

    /// <summary>The newer release, once one has been found. Null until then.</summary>
    public static Release? Available { get; private set; }

    /// <summary>Fires when <see cref="Available"/> has been filled in.</summary>
    public static event Action? Changed;

    /// <summary>Where the install has got to. Idle unless one was asked for.</summary>
    public static UpdateStage Stage { get; private set; }

    /// <summary>How much of the installer has arrived, 0 to 100.</summary>
    public static int Percent { get; private set; }

    /// <summary>Why the last attempt stopped. Null unless it failed.</summary>
    public static string? Failure { get; private set; }

    /// <summary>Fires whenever the three above change.</summary>
    public static event Action? ProgressChanged;

    /// <summary>
    /// Fires once the installer is running. Whoever listens has to quit the
    /// app. The installer replaces this executable and kills whatever still
    /// holds it, and a killed process leaves its tray icon behind.
    /// </summary>
    public static event Action? Installing;

    /// <summary>
    /// Asks GitHub what the newest release is.
    ///
    /// Called once when the app opens. Checking more often would tell somebody
    /// about a release in the middle of a game, which is the worst moment to
    /// hear about one.
    /// </summary>
    public static async Task CheckAsync()
    {
        if (!Enabled)
        {
            return;
        }

        var current = Parse(AppInfo.Version);
        if (current is null || current == new Version(0, 0, 0))
        {
            // A build from a checkout is not a release and is not behind one.
            return;
        }

        try
        {
            using var response = await Http.GetAsync(LatestRelease);
            if (!response.IsSuccessStatusCode)
            {
                return;
            }

            using var payload = JsonDocument.Parse(await response.Content.ReadAsStringAsync());
            var root = payload.RootElement;

            if (root.TryGetProperty("draft", out var draft) && draft.GetBoolean())
            {
                return;
            }
            if (!root.TryGetProperty("tag_name", out var tag) || tag.GetString() is not string name)
            {
                return;
            }

            var latest = Parse(name);
            if (latest is null || latest <= current)
            {
                // Nothing newer, so an installer still in the temporary folder
                // has already been applied.
                Discard();
                return;
            }

            var url = root.TryGetProperty("html_url", out var link) ? link.GetString() : null;
            Available = new Release(name.TrimStart('v', 'V'), url ?? ReleasesPage, FindInstaller(root));
            Changed?.Invoke();
        }
        catch (Exception error) when (error is HttpRequestException or TaskCanceledException or JsonException)
        {
            App.Trace($"update check failed: {error.Message}");
        }
    }

    /// <summary>
    /// Downloads the release's installer, runs it, and raises
    /// <see cref="Installing"/> so the app can quit.
    /// </summary>
    public static async Task InstallAsync()
    {
        if (Available?.Installer is not { } setup)
        {
            Report(UpdateStage.Failed, 0, "This release has no installer attached.");
            return;
        }
        if (Stage is UpdateStage.Downloading or UpdateStage.Starting)
        {
            return;
        }

        try
        {
            Report(UpdateStage.Downloading, 0, null);
            var installer = await DownloadAsync(setup);

            Report(UpdateStage.Starting, 100, null);
            // Starting it blocks until the consent prompt is answered, which
            // can be a minute. On this thread that is a window that has stopped
            // drawing.
            await Task.Run(() => Start(installer));

            Installing?.Invoke();
        }
        catch (Exception error) when (error is HttpRequestException or TaskCanceledException
            or IOException or UnauthorizedAccessException or Win32Exception)
        {
            App.Trace($"update install failed: {error}");
            Report(UpdateStage.Failed, 0, Explain(error));
        }
    }

    /// <summary>
    /// The installer attached to a release, if it has one. Matched on the
    /// ending rather than the full name, which carries the version. An asset
    /// still uploading has a download URL that answers 404, so it does not
    /// count.
    /// </summary>
    private static Setup? FindInstaller(JsonElement release)
    {
        if (!release.TryGetProperty("assets", out var assets) || assets.ValueKind != JsonValueKind.Array)
        {
            return null;
        }

        foreach (var asset in assets.EnumerateArray())
        {
            if (asset.TryGetProperty("state", out var state) && state.GetString() != "uploaded")
            {
                continue;
            }
            if (!asset.TryGetProperty("name", out var named) || named.GetString() is not string file
                || !file.EndsWith("-setup.exe", StringComparison.OrdinalIgnoreCase))
            {
                continue;
            }
            if (!asset.TryGetProperty("browser_download_url", out var link) || link.GetString() is not string url)
            {
                continue;
            }

            return new Setup(file, url, asset.TryGetProperty("size", out var size) ? size.GetInt64() : 0);
        }

        return null;
    }

    private static async Task<string> DownloadAsync(Setup setup)
    {
        Directory.CreateDirectory(Folder);
        var path = Path.Combine(Folder, setup.Name);

        // Left by an attempt that got as far as the consent prompt and was
        // refused. It is the same file, so it is not worth fetching twice.
        if (setup.Size > 0 && File.Exists(path) && new FileInfo(path).Length == setup.Size)
        {
            return path;
        }

        // Anything else here is a stale installer, or half of one.
        Discard();
        Directory.CreateDirectory(Folder);

        var partial = path + ".part";
        using var response = await Downloads.GetAsync(setup.Url, HttpCompletionOption.ResponseHeadersRead);
        response.EnsureSuccessStatusCode();

        var total = response.Content.Headers.ContentLength ?? setup.Size;

        await using (var source = await response.Content.ReadAsStreamAsync())
        await using (var file = new FileStream(partial, FileMode.Create, FileAccess.Write, FileShare.None))
        {
            var buffer = new byte[64 * 1024];
            long done = 0;
            int read;

            while ((read = await source.ReadAsync(buffer)) > 0)
            {
                await file.WriteAsync(buffer.AsMemory(0, read));
                done += read;

                if (total > 0)
                {
                    Report(UpdateStage.Downloading, (int)(done * 100 / total), null);
                }
            }
        }

        // Named only once it is whole, so a cut-off download is never taken for
        // a finished one.
        File.Move(partial, path, overwrite: true);
        return path;
    }

    /// <summary>
    /// Hands the installer to Windows. /SILENT is a progress window with
    /// nothing to click, since the person already chose to update. The
    /// installer asks for administrator rights through its own manifest, and
    /// UseShellExecute is what lets Windows show that prompt.
    /// </summary>
    private static void Start(string installer)
        => Process.Start(new ProcessStartInfo(installer)
        {
            Arguments = "/SILENT",
            UseShellExecute = true,
        });

    /// <summary>What to show when the install did not happen.</summary>
    private static string Explain(Exception error) => error switch
    {
        // ERROR_CANCELLED. The consent prompt was dismissed, which is a
        // decision and not a fault.
        Win32Exception windows when windows.NativeErrorCode == 1223
            => "Installing the update needs administrator rights.",
        TaskCanceledException => "The download took too long.",
        IOException or UnauthorizedAccessException => "The update could not be saved to this PC.",
        _ => "Could not download the update. Check your connection.",
    };

    /// <summary>Where downloaded installers wait to be run.</summary>
    private static string Folder => Path.Combine(Path.GetTempPath(), "192168-update");

    /// <summary>Deletes downloaded installers. They are over a hundred megabytes each.</summary>
    private static void Discard()
    {
        try
        {
            if (Directory.Exists(Folder))
            {
                Directory.Delete(Folder, recursive: true);
            }
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            App.Trace($"could not clear old installers: {error.Message}");
        }
    }

    private static void Report(UpdateStage stage, int percent, string? failure)
    {
        if (stage == Stage && percent == Percent && failure == Failure)
        {
            return;
        }

        Stage = stage;
        Percent = percent;
        Failure = failure;
        ProgressChanged?.Invoke();
    }

    /// <summary>
    /// Reads a version out of a tag. Anything after the numbers is ignored,
    /// because a suffix says which build of a version it is and not which
    /// version.
    /// </summary>
    private static Version? Parse(string text)
    {
        var core = text.TrimStart('v', 'V').Split('-', '+')[0];
        return Version.TryParse(core, out var version) ? version : null;
    }

    private static HttpClient CreateClient(TimeSpan timeout)
    {
        var client = new HttpClient { Timeout = timeout };
        // GitHub turns away requests with no user agent.
        client.DefaultRequestHeaders.UserAgent.Add(new ProductInfoHeaderValue("192168", AppInfo.Version));
        client.DefaultRequestHeaders.Accept.Add(new MediaTypeWithQualityHeaderValue("application/vnd.github+json"));
        return client;
    }
}
