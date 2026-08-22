using System.IO;
using System.IO.Compression;
using System.Text;
using Net192168.Client.Ipc;

namespace Net192168.Client.Services;

/// <summary>
/// Packs the logs into one file to hand to somebody.
///
/// A report worth acting on needs the daemon's log, this app's, the packet log
/// if it was running, and enough about the machine to tell which of them is
/// unusual. Asking for that a file at a time gets one of them, usually the
/// wrong one, so it is one button and one zip.
/// </summary>
public static class LogExport
{
    /// <summary>What the saved file is called by default, dated so two of them
    /// from different days do not collide in a downloads folder.</summary>
    public static string SuggestedName => $"192168-logs-{DateTime.Now:yyyy-MM-dd-HHmm}";

    /// <summary>
    /// Writes a zip of every log to <paramref name="destination"/> and reports
    /// how many of them there were.
    ///
    /// A log that was never written is skipped rather than added empty: a zip
    /// with no packets.log in it says the packet log was off, which is worth
    /// knowing, and one with an empty packets.log says nothing.
    /// </summary>
    public static async Task<int> WriteAsync(string destination, DaemonState state, bool packetLogEnabled)
    {
        var written = 0;

        // Created rather than appended to. Exporting twice to the same name is
        // a second attempt, not a second copy.
        using (var file = new FileStream(destination, FileMode.Create, FileAccess.Write, FileShare.None))
        using (var zip = new ZipArchive(file, ZipArchiveMode.Create))
        {
            foreach (var path in Logs())
            {
                if (await CopyIntoAsync(zip, path))
                {
                    written++;
                }
            }

            var entry = zip.CreateEntry("system.txt", CompressionLevel.Optimal);
            await using var describing = new StreamWriter(entry.Open(), Encoding.UTF8);
            await describing.WriteAsync(Describe(state, packetLogEnabled));
        }

        return written;
    }

    /// <summary>
    /// Every log file, current and the one generation behind each, in the order
    /// somebody reading them would want.
    /// </summary>
    private static IEnumerable<string> Logs()
    {
        foreach (var name in new[] { "daemon.log", "packets.log" })
        {
            var path = Path.Combine(App.LogFolder, name);
            yield return path;
            yield return path + ".1";
        }

        // Resolved rather than assumed to be in the folder above: a development
        // build with no service to create ProgramData writes this one to the
        // user's own folder instead.
        yield return App.LogPath;
        yield return App.PreviousLogPath;
    }

    /// <summary>
    /// Copies one log into the zip, reporting whether there was one.
    ///
    /// Read with the widest sharing there is, because the daemon holds its logs
    /// open for writing and a plain File.Open would be refused every time.
    /// </summary>
    private static async Task<bool> CopyIntoAsync(ZipArchive zip, string path)
    {
        try
        {
            await using var source = new FileStream(
                path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);

            var entry = zip.CreateEntry(Path.GetFileName(path), CompressionLevel.Optimal);
            await using var target = entry.Open();
            await source.CopyToAsync(target);
            return true;
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            // A log that is not there is the ordinary case, and one that cannot
            // be read is not worth failing the whole export over.
            return false;
        }
    }

    /// <summary>
    /// What the logs cannot say about themselves: which build wrote them, what
    /// they were running on, and what the app thought was connected.
    ///
    /// This is the first round of questions on every report, so it is worth the
    /// twenty lines it takes to answer them up front.
    /// </summary>
    private static string Describe(DaemonState state, bool packetLogEnabled)
    {
        var about = new StringBuilder();
        about.AppendLine($"exported     {DateTimeOffset.Now:O}");
        about.AppendLine($"app          {AppInfo.Version}");
        about.AppendLine($"windows      {Environment.OSVersion.VersionString}");
        about.AppendLine($"architecture {System.Runtime.InteropServices.RuntimeInformation.OSArchitecture}");
        about.AppendLine($"logs         {App.LogFolder}");
        about.AppendLine($"packet log   {(packetLogEnabled ? "on" : "off")}");
        about.AppendLine();

        about.AppendLine($"daemon       {state.Connection}");
        about.AppendLine($"server       {state.ServerUrl} ({(state.ServerOnline ? "reachable" : "unreachable")})");
        about.AppendLine($"nickname     {state.Nickname}");
        about.AppendLine($"group        {state.GroupName ?? "none"}");
        about.AppendLine($"virtual ip   {state.VirtualIp ?? "none"}");
        about.AppendLine($"packets      {state.PacketsSent} sent, {state.PacketsReceived} received");
        if (!string.IsNullOrEmpty(state.Message))
        {
            about.AppendLine($"message      {state.Message}");
        }

        // The peer list is where a report about one person not working gets its
        // answer, so it goes in whole rather than as a count.
        about.AppendLine();
        about.AppendLine($"peers ({state.Peers.Count})");
        foreach (var peer in state.Peers)
        {
            var latency = peer.LatencyMs is { } ms ? $"{ms}ms" : "-";
            var why = peer.Reason == PeerReason.None ? "" : $" ({peer.Reason})";
            about.AppendLine(
                $"  {peer.VirtualIp,-12} {peer.State,-10}{why} {latency,-7} " +
                $"{peer.PacketsSent} sent, {peer.PacketsReceived} received  {peer.Nickname}");
        }

        return about.ToString();
    }
}
