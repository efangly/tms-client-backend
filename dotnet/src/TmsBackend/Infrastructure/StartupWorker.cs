namespace TmsBackend.Infrastructure;

/// <summary>
/// Runs the dependency bring-up from main.go's startServer in the background:
/// wait for the network, then (Phase 2) connect to the database with retry,
/// then mark the app ready so polling/archiving can begin.
/// </summary>
public sealed class StartupWorker(AppReadiness readiness, ILogger<StartupWorker> logger) : BackgroundService
{
    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        logger.LogInformation("Waiting for network connectivity...");
        if (!await Startup.WaitForNetworkAsync(TimeSpan.FromSeconds(60), logger, stoppingToken))
            logger.LogError("Network not ready after timeout, continuing anyway");

        readiness.MarkReady();
        logger.LogInformation("Startup complete");
    }
}
