using TmsBackend.Config;
using TmsBackend.Data;

namespace TmsBackend.Infrastructure;

/// <summary>
/// Runs the dependency bring-up from main.go's startServer in the background:
/// wait for the network, connect to the database with retry, then mark the
/// app ready so polling/archiving can begin.
/// </summary>
public sealed class StartupWorker(Db db, AppOptions options, AppReadiness readiness, ILogger<StartupWorker> logger) : BackgroundService
{
    private static readonly TimeSpan ReconnectInterval = TimeSpan.FromSeconds(30);

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        logger.LogInformation("Waiting for network connectivity...");
        if (!await Startup.WaitForNetworkAsync(TimeSpan.FromSeconds(60), logger, stoppingToken))
            logger.LogError("Network not ready after timeout, continuing anyway");

        logger.LogInformation("Connecting to database {Host}:{Port}/{Name} as {User} (DB_CHARSET={Charset} ignored: connection is utf8mb4, server converts per column)",
            options.DbHost, options.DbPort, options.DbName, options.DbUser, options.DbCharset);
        try
        {
            await Startup.RetryWithBackoffAsync("Database connection", db.ConnectAsync,
                maxAttempts: 5, TimeSpan.FromSeconds(2), TimeSpan.FromSeconds(30), logger, stoppingToken);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            // Go gave up here and the server never started. A service must not
            // stay dead until someone restarts it, so keep trying in the background.
            logger.LogError("Failed to connect to database after retries: {Error}. Retrying every {Interval}", e.Message, ReconnectInterval);
            while (true)
            {
                await Task.Delay(ReconnectInterval, stoppingToken);
                try
                {
                    await db.ConnectAsync(stoppingToken);
                    break;
                }
                catch (Exception retry) when (retry is not OperationCanceledException)
                {
                    logger.LogWarning("Database connection failed: {Error}", retry.Message);
                }
            }
        }
        logger.LogInformation("Database connected successfully");

        readiness.MarkReady();
        logger.LogInformation("Startup complete");
    }
}
