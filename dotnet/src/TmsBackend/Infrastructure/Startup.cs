using System.Net.NetworkInformation;
using System.Net.Sockets;

namespace TmsBackend.Infrastructure;

/// <summary>Startup helpers ported from internal/utils/network.go.</summary>
public static class Startup
{
    /// <summary>Non-loopback IPv4 addresses of this machine.</summary>
    public static IReadOnlyList<string> LocalIPv4() =>
        NetworkInterface.GetAllNetworkInterfaces()
            .Where(n => n.OperationalStatus == OperationalStatus.Up)
            .SelectMany(n => n.GetIPProperties().UnicastAddresses)
            .Where(a => a.Address.AddressFamily == AddressFamily.InterNetwork && !System.Net.IPAddress.IsLoopback(a.Address))
            .Select(a => a.Address.ToString())
            .ToList();

    /// <summary>
    /// Waits until some interface has a non-loopback IPv4 address (important
    /// right after boot, when the service may start before the network).
    /// </summary>
    public static async Task<bool> WaitForNetworkAsync(TimeSpan timeout, ILogger logger, CancellationToken ct)
    {
        logger.LogInformation("Checking network connectivity...");
        var deadline = DateTime.UtcNow + timeout;
        for (var attempt = 1; DateTime.UtcNow < deadline; attempt++)
        {
            var ips = LocalIPv4();
            if (ips.Count > 0)
            {
                logger.LogInformation("Network is ready (attempt {Attempt}, IP: {Ip})", attempt, ips[0]);
                return true;
            }
            logger.LogInformation("Network not ready yet (attempt {Attempt}), retrying...", attempt);
            await Task.Delay(TimeSpan.FromSeconds(2), ct);
        }
        logger.LogWarning("Network check timeout after {Timeout}", timeout);
        return false;
    }

    /// <summary>Retries <paramref name="action"/> with exponential backoff; rethrows the last error.</summary>
    public static async Task RetryWithBackoffAsync(
        string operation,
        Func<CancellationToken, Task> action,
        int maxAttempts,
        TimeSpan initialDelay,
        TimeSpan maxDelay,
        ILogger logger,
        CancellationToken ct)
    {
        var delay = initialDelay;
        for (var attempt = 1; ; attempt++)
        {
            logger.LogInformation("{Operation} (attempt {Attempt}/{Max})...", operation, attempt, maxAttempts);
            try
            {
                await action(ct);
                if (attempt > 1)
                    logger.LogInformation("{Operation} succeeded after {Attempt} attempts", operation, attempt);
                return;
            }
            catch (Exception e) when (e is not OperationCanceledException && attempt < maxAttempts)
            {
                logger.LogWarning("{Operation} failed: {Error}", operation, e.Message);
                logger.LogInformation("Retrying in {Delay}...", delay);
                await Task.Delay(delay, ct);
                delay = TimeSpan.FromTicks(Math.Min(delay.Ticks * 2, maxDelay.Ticks));
            }
        }
    }
}
