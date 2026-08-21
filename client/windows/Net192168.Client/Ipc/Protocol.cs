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
    public string? Nickname { get; init; }
    public string? VirtualIp { get; init; }

    /// <summary>Whether this device runs the connected group. The server is what
    /// enforces that; this only decides what is on screen.</summary>
    public bool IsOwner { get; init; }
    public IReadOnlyList<PeerView> Peers { get; init; } = [];
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

    /// <summary>Whoever runs the group, marked in the list.</summary>
    public bool IsOwner { get; init; }
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
    public string Nickname { get; init; } = "";
    public bool Active { get; init; }
    public int? OnlineMembers { get; init; }

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

public sealed record TestServerResult
{
    public bool Reachable { get; init; }
    public int Version { get; init; }
    public string? Message { get; init; }
}

/// <summary>Creates a group, with the look it is made with.</summary>
public sealed record CreateGroupParams(string Name, string Password, string Nickname, string Icon, string Color);

public sealed record JoinGroupParams(string Group, string Password, string Nickname);

public sealed record GroupParams(string GroupId);

public sealed record SetNicknameParams(string GroupId, string Nickname);

public sealed record ServerParams(string Url);

/// <summary>Turns LAN discovery on or off.</summary>
public sealed record LanDiscoveryParams(bool Enabled);

/// <summary>Names one person in one group.</summary>
public sealed record MemberParams(string GroupId, string DeviceId);

/// <summary>Changes what a group is called, for everyone in it.</summary>
public sealed record RenameGroupParams(string GroupId, string Name);

/// <summary>Changes the icon and colour a group is shown with, for everyone in
/// it. Both travel together, because they are picked together.</summary>
public sealed record SetGroupAppearanceParams(string GroupId, string Icon, string Color);

/// <summary>Changes the password a new member joins with.</summary>
public sealed record SetGroupPasswordParams(string GroupId, string Password);
