using System.Collections.Concurrent;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Infrastructure;

namespace TmsBackend.Transport.Serial;

/// <summary>
/// Keeps a persistent, auto-reconnecting connection open for every COM-attached
/// probe (machine_ip starting with "COM"), so polling reuses an open port
/// instead of dialing per request (services.SerialService).
/// </summary>
public sealed class SerialService(
    IMachineRepository machines,
    AppOptions options,
    AppReadiness readiness,
    ILoggerFactory loggerFactory) : BackgroundService
{
    private readonly ConcurrentDictionary<string, SerialConnection> _connections = new();
    private readonly ILogger _logger = loggerFactory.CreateLogger<SerialService>();

    /// <summary>Opens real ports; tests swap in fakes.</summary>
    internal Func<SerialPortConfig, ISerialPortIo> PortFactory { get; set; } = c => new SystemSerialPortIo(c);

    /// <summary>Devices are wired up as serial when machine_ip starts with "COM" (case-insensitive).</summary>
    public static bool IsSerialPath(string machineIp) => machineIp.StartsWith("COM", StringComparison.OrdinalIgnoreCase);

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        await readiness.WaitAsync(stoppingToken);
        _logger.LogInformation("Initializing serial service...");
        try
        {
            await OpenConfiguredPortsAsync(stoppingToken);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            _logger.LogError("Serial service start failed: {Error} (continuing without serial devices)", e.Message);
        }
    }

    /// <summary>Opens one connection per distinct COM machine_ip found in master_machine.</summary>
    internal async Task OpenConfiguredPortsAsync(CancellationToken ct)
    {
        var all = await machines.GetAllAsync(ct);
        var paths = all.Select(m => m.MachineIP).Where(IsSerialPath).Distinct().ToList();

        // Opening blocks for the boot-settle delay, so do the ports in parallel off the request path.
        var results = await Task.WhenAll(paths.Select(path => Task.Run(() =>
        {
            var conn = Track(path);
            try
            {
                conn.Connect();
                return true;
            }
            catch (Exception e)
            {
                _logger.LogWarning("Serial {Path}: initial connect failed, will keep retrying via auto-reconnect: {Error}", path, e.Message);
                return false;
            }
        }, ct)));

        _logger.LogInformation("Serial service: opened {Opened}/{Total} configured COM port(s)", results.Count(ok => ok), paths.Count);
    }

    /// <summary>Returns the tracked connection for <paramref name="path"/>, creating (not opening) it if new.</summary>
    internal SerialConnection Track(string path) =>
        _connections.GetOrAdd(path, p => new SerialConnection(new SerialPortConfig
        {
            Path = p,
            BaudRate = options.DefaultSerialBaud,
            AutoReconnect = true,
            BootSettleDelay = options.DefaultSerialBootDelay,
        }, PortFactory, loggerFactory.CreateLogger($"TmsBackend.Serial.{p}")));

    /// <summary>The persistent connection for a COM path, if it was in master_machine at startup.</summary>
    public SerialConnection? GetConnection(string path) => _connections.GetValueOrDefault(path);

    public override async Task StopAsync(CancellationToken cancellationToken)
    {
        await base.StopAsync(cancellationToken);
        foreach (var conn in _connections.Values)
            conn.Dispose();
        _connections.Clear();
    }
}
