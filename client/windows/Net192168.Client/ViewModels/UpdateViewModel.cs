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
    public partial bool HasUpdate { get; set; }

    [ObservableProperty]
    [NotifyPropertyChangedFor(nameof(Title))]
    [NotifyPropertyChangedFor(nameof(Label))]
    public partial string? Version { get; set; }

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
    [NotifyPropertyChangedFor(nameof(Detail))]
    public partial bool IsRequired { get; set; }

    /// <summary>Why it is needed, for the banner. Empty for an ordinary one,
    /// which does not need explaining.</summary>
    public string Detail => IsRequired
        ? "This version changes how the app talks to the server. Until you update, you can still connect to your groups but not create or join one."
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
        OnPropertyChanged(nameof(Url));
    }

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
