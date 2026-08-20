namespace Net192168.Client.Ipc;

/// <summary>
/// What a player is told when something fails.
///
/// The daemon and the server both write messages for a person, and either would
/// be safe to show as it is. They are written from where the failure happened
/// though, so they describe a device, a token, or a session. The stable code is
/// the part worth relying on, and this turns it into a sentence about the thing
/// the player was actually trying to do.
///
/// A code with no entry falls back to whatever came with it. That is the right
/// default: a newer daemon can add one, and an unmapped code still says
/// something true rather than "an error occurred".
/// </summary>
public static class ErrorCopy
{
    /// <summary>
    /// The sentence to show, and the detail to log.
    ///
    /// The caller shows the first and logs the second, so a support question
    /// can be answered without putting a code in front of a player.
    /// </summary>
    public static string Describe(DaemonException error, ErrorContext context = ErrorContext.General)
    {
        App.Trace($"daemon error: code={error.Code} message={error.Message}");
        return Copy(error, context) ?? error.Message;
    }

    private static string? Copy(DaemonException error, ErrorContext context) => error.Code switch
    {
        // The server answers a wrong name and a wrong password the same way on
        // purpose, so that joining cannot be used to find out which groups
        // exist. The copy has to keep that promise.
        "invalid_password" => "That group name or password is not right.",

        "group_name_taken" => "A group with that name already exists. Pick another.",

        // Reached by connecting to or leaving a group this device was removed
        // from, which reads as the group being gone.
        "group_not_found" => context == ErrorContext.Join
            ? "That group name or password is not right."
            : "You are not in that group any more.",

        "membership_revoked" => "You were removed from that group.",

        "group_full" => "That group is full. Someone has to disconnect first.",

        // The daemon registers again by itself when a token stops working, so
        // this only survives when that failed too.
        "unauthorized" => "This device could not sign in to the server.",

        "session_invalid" => "The connection to the group ended.",

        "rate_limited" => "Too many tries. Wait a moment, then try again.",

        // A self-hosted server can be older or newer than the app.
        "version_unsupported" => "That server does not work with this version of the app.",

        "unreachable" => "Could not reach the server. Check your connection, or the address under Settings.",

        "no_server" => "No server is set. Add one under Settings.",

        "disconnected" => "The background service is not running. Start it under Settings.",

        "busy" => "Still working on the last thing. Give it a moment.",

        // bad_request is the daemon checking its own input, and those messages
        // already name the field. Anything else is a bug, and its text belongs
        // in the log rather than on screen.
        "internal" or "unknown_method" => "Something went wrong. The details are in the log.",

        _ => null,
    };
}

/// <summary>
/// Which screen asked, for the codes that mean different things depending on
/// what the player was doing.
/// </summary>
public enum ErrorContext
{
    General,
    Join,
}
