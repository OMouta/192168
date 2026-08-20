using System.Collections.ObjectModel;
using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Net192168.Client.Ipc;

namespace Net192168.Client.ViewModels;

/// <summary>One peer as the list shows it.</summary>
public sealed record PeerItem(string Nickname, string VirtualIp, string Status);

/// <summary>The screen you watch while playing.</summary>
public sealed partial class ActiveGroupViewModel : ObservableObject
{
    private readonly Daemon _daemon;

    public ActiveGroupViewModel(Daemon daemon)
    {
        _daemon = daemon;
        _daemon.StateChanged += Update;
        Update();
    }

    public ObservableCollection<PeerItem> Peers { get; } = [];

    [ObservableProperty]
    public partial bool IsConnected { get; set; }

    [ObservableProperty]
    public partial string? GroupName { get; set; }

    [ObservableProperty]
    public partial string? Status { get; set; }

    [ObservableProperty]
    public partial string? Nickname { get; set; }

    [ObservableProperty]
    public partial string? VirtualIp { get; set; }

    [ObservableProperty]
    public partial bool HasPeers { get; set; }

    [ObservableProperty]
    public partial string? Message { get; set; }

    [RelayCommand]
    public async Task DisconnectAsync()
    {
        try
        {
            await _daemon.DisconnectAsync();
        }
        catch (DaemonException e)
        {
            Message = e.Message;
        }
    }

    private void Update()
    {
        var state = _daemon.State;

        IsConnected = state.Connection == ConnectionState.Connected;
        GroupName = state.GroupName ?? "";
        Nickname = state.Nickname ?? "";
        VirtualIp = state.VirtualIp ?? "";
        Message = state.Message;
        Status = Describe(state.Connection);

        Peers.Clear();
        foreach (var peer in state.Peers)
        {
            Peers.Add(new PeerItem(peer.Nickname, peer.VirtualIp, Describe(peer)));
        }
        HasPeers = Peers.Count > 0;
    }

    private static string Describe(ConnectionState connection) => connection switch
    {
        ConnectionState.Connected => "Connected",
        ConnectionState.Connecting => "Connecting",
        ConnectionState.Disconnecting => "Disconnecting",
        ConnectionState.Error => "Something went wrong",
        _ => "Not connected",
    };

    /// <summary>
    /// What a player is told about one peer. The words describe whether they can
    /// be reached, not how the connection was made.
    /// </summary>
    private static string Describe(PeerView peer) => peer.State switch
    {
        PeerState.Direct when peer.LatencyMs is int ms => $"Direct · {ms} ms",
        PeerState.Direct => "Direct",
        PeerState.Indirect => "Relayed",
        PeerState.Connecting => "Connecting",
        PeerState.Failed => "Unreachable",
        _ => "Offline",
    };
}
