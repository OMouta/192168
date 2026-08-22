using System.Text.Json;
using System.Text.Json.Serialization;

namespace Net192168.Client.Ipc;

/// <summary>
/// The shapes the daemon speaks. These mirror protocol/ipc on the Go side, and
/// the field names are the contract between them.
/// </summary>
public static class Protocol
{
    public const string PipeName = "192168";

    /// <summary>
    /// Both sides use camelCase names and lowercase enum values.
    /// </summary>
    public static readonly JsonSerializerOptions Json = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
        PropertyNameCaseInsensitive = true,
        Converters = { new JsonStringEnumConverter(JsonNamingPolicy.CamelCase) },
    };
}

/// <summary>The daemon's overall status. One group is active at a time.</summary>
public enum ConnectionState
{
    Disconnected,
    Connecting,
    Connected,
    Disconnecting,
    Error,
}

/// <summary>
/// The state of one peer link. Links are independent: failing to reach one peer
/// says nothing about the others.
/// </summary>
public enum PeerState
{
    Connecting,
    Direct,
    Indirect,
    Offline,
    Failed,
}

/// <summary>
/// What is standing in the way of a link. None for one with nothing wrong.
/// </summary>
public enum PeerReason
{
    None,
    /// <summary>They are online, but nobody has been told where to reach them.</summary>
    NoAddress,
    /// <summary>Neither network would let a connection through, and nobody else
    /// in the group could carry it.</summary>
    NoRoute,
}

/// <summary>Everything needed to draw the app.</summary>
public sealed record DaemonState
{
    public ConnectionState Connection { get; init; }
    public string ServerUrl { get; init; } = "";
    public bool ServerOnline { get; init; }
    public string? GroupId { get; init; }
    public string? GroupName { get; init; }

    /// <summary>The connected group's look, so the screen showing it is marked
    /// the same way its row in the list was.</summary>
    public string? GroupIcon { get; init; }
    public string? GroupColor { get; init; }
    /// <summary>What this device is called, in every group. Set whether or not
    /// one is connected.</summary>
    public string? Nickname { get; init; }
    public string? VirtualIp { get; init; }

    /// <summary>Whether this device runs the connected group. The server is what
    /// enforces that; this only decides what is on screen.</summary>
    public bool IsOwner { get; init; }
    public IReadOnlyList<PeerView> Peers { get; init; } = [];

    /// <summary>Counted at the adapter rather than per link, so it is not the
    /// sum of the peers. A packet addressed to the whole LAN is one here and one
    /// on every link.</summary>
    public ulong PacketsSent { get; init; }
    public ulong PacketsReceived { get; init; }
    public string? Message { get; init; }
}

/// <summary>One row of the active group screen.</summary>
public sealed record PeerView
{
    public string DeviceId { get; init; } = "";
    public string Nickname { get; init; } = "";
    public string VirtualIp { get; init; } = "";
    public PeerState State { get; init; }
    public int? LatencyMs { get; init; }

    /// <summary>What is in the way, if anything.</summary>
    public PeerReason Reason { get; init; }

    /// <summary>Whoever runs the group, marked in the list.</summary>
    public bool IsOwner { get; init; }

    /// <summary>The game's packets each way, without the handshakes or
    /// keepalives, so zero on an open link means nothing is using it.</summary>
    public ulong PacketsSent { get; init; }
    public ulong PacketsReceived { get; init; }
}

/// <summary>One saved membership.</summary>
public sealed record Group
{
    public string GroupId { get; init; } = "";
    public string Name { get; init; } = "";

    /// <summary>The look the owner picked, as keys the app maps to a glyph and
    /// a colour. Empty means the default one.</summary>
    public string Icon { get; init; } = "";
    public string Color { get; init; } = "";
    public bool Active { get; init; }
    public int? OnlineMembers { get; init; }

    /// <summary>The group's code, and that code as a link. Both empty unless
    /// this device owns the group, which the server decides.</summary>
    public string InviteCode { get; init; } = "";
    public string InviteLink { get; init; } = "";

    /// <summary>Whether this device runs the group, which decides whether the
    /// row offers a way into its settings.</summary>
    public bool IsOwner { get; init; }
}

public sealed record GetGroupsResult
{
    public IReadOnlyList<Group> Groups { get; init; } = [];
}

public sealed record GroupResult
{
    public Group Group { get; init; } = new();
}

public sealed record GetServerResult
{
    public string Url { get; init; } = "";
}

public sealed record LanDiscoveryResult
{
    public bool Enabled { get; init; }
}

public sealed record PacketLogResult
{
    public bool Enabled { get; init; }
}

/// <summary>What was emptied. A log that was never written is not listed and is
/// not a failure.</summary>
public sealed record ClearLogsResult
{
    public IReadOnlyList<string> Cleared { get; init; } = [];
}

public sealed record TestServerResult
{
    public bool Reachable { get; init; }
    public int Version { get; init; }
    public string? Message { get; init; }
}

/// <summary>Creates a group, with the look it is made with.</summary>
public sealed record CreateGroupParams(string Name, string Icon, string Color);

/// <summary>Joins whichever group an invite opens. Code is whatever was pasted,
/// bare or as a link.</summary>
public sealed record JoinGroupParams(string Code);

/// <summary>Asks what a code opens without acting on it.</summary>
public sealed record InviteParams(string Code);

/// <summary>What an invite opens. Found is false for a code that opens nothing,
/// which is an answer rather than an error: half-typed codes are invalid.</summary>
public sealed record InviteResult
{
    public bool Found { get; init; }
    public string Code { get; init; } = "";
    public string GroupName { get; init; } = "";
    public string GroupIcon { get; init; } = "";
    public string GroupColor { get; init; } = "";
    public int Members { get; init; }
}

/// <summary>A group's new code, and the link to it.</summary>
public sealed record InviteCodeResult
{
    public string Code { get; init; } = "";
    public string Link { get; init; } = "";
}

public sealed record GroupParams(string GroupId);

public sealed record SetNicknameParams(string Nickname);

public sealed record ServerParams(string Url);

/// <summary>Turns LAN discovery on or off.</summary>
public sealed record LanDiscoveryParams(bool Enabled);

/// <summary>Turns the packet log on or off.</summary>
public sealed record PacketLogParams(bool Enabled);

/// <summary>Names one person in one group.</summary>
public sealed record MemberParams(string GroupId, string DeviceId);

/// <summary>One person on the connected network. No group, because only one is
/// connected and a link belongs to that one.</summary>
public sealed record PeerParams(string DeviceId);

/// <summary>Changes what a group is called, for everyone in it.</summary>
public sealed record RenameGroupParams(string GroupId, string Name);

/// <summary>Changes the icon and colour a group is shown with, for everyone in
/// it. Both travel together, because they are picked together.</summary>
public sealed record SetGroupAppearanceParams(string GroupId, string Icon, string Color);

