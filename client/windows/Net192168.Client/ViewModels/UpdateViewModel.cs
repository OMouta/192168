using CommunityToolkit.Mvvm.ComponentModel;
using CommunityToolkit.Mvvm.Input;
using Net192168.Client.Services;

namespace Net192168.Client.ViewModels;

/// <summary>
/// The update, on both screens that show it. There is one release and one
/// download of it, so there is one of these. An instance each would let home
/// and About disagree about how far along it is.
/// </summary>
public sealed partial class UpdateViewModel : ObservableObject
{
    /// <summary>The only instance.</summary>
    public static UpdateViewModel Current { get; } = new();

    private UpdateViewModel()
    {
        // The check runs once at launch and can land after a screen is built.
        Updates.Changed += Show;
        Updates.ProgressChanged += Show;
        Show();
    }

    /// <summary>Whether there is a newer version.</summary>
    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(InstallCommand))]
    [NotifyPropertyChangedFor(nameof(Headline))]
    public partial bool HasUpdate { get; set; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Title))]
    [NotifyPropertyChangedFor(nameof(Headline))]
    [NotifyPropertyChangedFor(nameof(Label))]
    public partial string? Version { get; set; }

    /// <summary>
    /// The one line at the top of the Updates tab, whichever way the last check
    /// went. An app that is current has as much to say as one that is behind.
    /// </summary>
    public string Headline => HasUpdate ? Title : CheckSummary;

    public string Title => IsRequired
        ? $"Version {Version} is needed"
        : $"Version {Version} is available";

    /// <summary>
    /// Whether the server has moved on past this build. The banner is a warning
    /// rather than a note when it has, because putting it off leaves an app that
    /// cannot create or join a group.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Title))]
    [NotifyPropertyChangedFor(nameof(Headline))]
    [NotifyPropertyChangedFor(nameof(Detail))]
    public partial bool IsRequired { get; set; }

    /// <summary>Why it is needed, for the banner. Empty for an ordinary one,
    /// which does not need explaining.</summary>
    public string Detail => IsRequired
        ? "Update or the app might not work as expected."
        : "";

    /// <summary>What the button says on About, where no title above it names
    /// the version.</summary>
    public string Label => $"Update to {Version}";

    /// <summary>True while the installer is downloading or starting.</summary>
    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(InstallCommand))]
    public partial bool IsWorking { get; set; }

    /// <summary>True only while there is a number to show.</summary>
    [ObservableProperty]
    public partial bool IsDownloading { get; set; }

    /// <summary>How much of the installer has arrived, 0 to 100.</summary>
    [ObservableProperty]
    public partial double Percent { get; set; }

    /// <summary>One line under the button. Empty until it is pressed.</summary>
    [ObservableProperty]
    public partial string? Status { get; set; }

    /// <summary>Whether the last attempt failed.</summary>
    [ObservableProperty]
    public partial bool HasFailed { get; set; }

    /// <summary>The release page, for doing it by hand.</summary>
    public Uri Url => new(Updates.Available?.Url ?? Updates.ReleasesPage);

    /// <summary>Fetches the installer and runs it, which closes the app.</summary>
    [RelayCommand(CanExecute = nameof(CanInstall))]
    private Task InstallAsync() => Updates.InstallAsync();

    private bool CanInstall() => HasUpdate && !IsWorking;

    /// <summary>Whether a check is in flight, which is what greys the button.</summary>
    [ObservableProperty]
    [NotifyCanExecuteChangedFor(nameof(CheckCommand))]
    public partial bool IsChecking { get; set; }

    /// <summary>
    /// What the last check found, for the screen that has a button to run one.
    ///
    /// A check that comes back with nothing has to say so. Without this the
    /// button looks like it did nothing, and the obvious next move is to press
    /// it again.
    /// </summary>
    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Headline))]
    public partial string CheckSummary { get; set; } = "";

    /// <summary>
    /// Looks now, whether or not the app is set to look on its own. Pressing a
    /// button is not the app reaching out unprompted.
    /// </summary>
    [RelayCommand(CanExecute = nameof(CanCheck))]
    private async Task CheckAsync()
    {
        IsChecking = true;
        try
        {
            await Updates.CheckAsync(asked: true);
        }
        finally
        {
            IsChecking = false;
        }
    }

    private bool CanCheck() => !IsChecking;

    private void Show()
    {
        Version = Updates.Available?.Version;
        HasUpdate = Version is not null;
        IsRequired = Updates.Available?.Breaking ?? false;
        IsDownloading = Updates.Stage is UpdateStage.Downloading;
        IsWorking = Updates.Stage is UpdateStage.Downloading or UpdateStage.Starting;
        HasFailed = Updates.Stage is UpdateStage.Failed;
        Percent = Updates.Percent;
        Status = Describe();
        CheckSummary = Summarise();
        OnPropertyChanged(nameof(Url));
    }

    /// <summary>
    /// One line about the last look, as opposed to <see cref="Status"/>, which
    /// is about the download.
    /// </summary>
    private static string Summarise() => Updates.LastCheck switch
    {
        Updates.CheckOutcome.UpToDate => $"192168 {AppInfo.Version} is the latest version.",
        Updates.CheckOutcome.Unreachable => "Could not reach GitHub to check.",
        // A build from a checkout has no release to be behind, and saying so is
        // more use than an error nobody can act on.
        Updates.CheckOutcome.NotARelease => "This is a development build, so there is nothing to update to.",
        // Never looked, or looked and found something, in which case the
        // headline names the version rather than this.
        _ => $"192168 {AppInfo.Version}",
    };

    private static string? Describe() => Updates.Stage switch
    {
        // The download is the only stage that lasts long enough to read, so it
        // is where the warning goes. Installing drops the tunnel.
        UpdateStage.Downloading => $"Downloading, {Updates.Percent}%. 192168 closes to install.",
        UpdateStage.Starting => "Installing. 192168 opens again when it is done.",
        UpdateStage.Failed => Updates.Failure,
        _ => null,
    };
}
