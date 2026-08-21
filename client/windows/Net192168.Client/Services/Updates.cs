using System.Net.Http;
using System.Net.Http.Headers;
using System.Text.Json;

namespace Net192168.Client.Services;

/// <summary>A release newer than this one.</summary>
public sealed record Release(string Version, string Url);

/// <summary>
/// Whether there is a newer version, and where to get it.
///
/// It looks and says so. It does not download anything and does not replace
/// anything: an app that rewrites itself while a service it installed is
/// running is a much bigger idea than this needs, and one nobody asked for.
///
/// A check that fails says nothing. No network, a rate limit, a rewritten API:
/// none of that is the user's problem, and an update checker that complains is
/// worse than one that stays quiet.
/// </summary>
public static class Updates
{
    private const string LatestRelease = "https://api.github.com/repos/OMouta/192168/releases/latest";

    private static readonly HttpClient Http = CreateClient();

    /// <summary>The newer release, once one has been found. Null until then.</summary>
    public static Release? Available { get; private set; }

    /// <summary>Fires when <see cref="Available"/> has been filled in.</summary>
    public static event Action? Changed;

    /// <summary>
    /// Asks GitHub what the newest release is.
    ///
    /// Called once when the app opens. Checking more often would tell somebody
    /// about a release in the middle of a game, which is the worst moment to
    /// hear about one.
    /// </summary>
    public static async Task CheckAsync()
    {
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
                return;
            }

            var url = root.TryGetProperty("html_url", out var link) ? link.GetString() : null;
            Available = new Release(name.TrimStart('v', 'V'), url ?? "https://github.com/OMouta/192168/releases");
            Changed?.Invoke();
        }
        catch (Exception error) when (error is HttpRequestException or TaskCanceledException or JsonException)
        {
            App.Trace($"update check failed: {error.Message}");
        }
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

    private static HttpClient CreateClient()
    {
        var client = new HttpClient { Timeout = TimeSpan.FromSeconds(10) };
        // GitHub turns away requests with no user agent.
        client.DefaultRequestHeaders.UserAgent.Add(new ProductInfoHeaderValue("192168", AppInfo.Version));
        client.DefaultRequestHeaders.Accept.Add(new MediaTypeWithQualityHeaderValue("application/vnd.github+json"));
        return client;
    }
}
