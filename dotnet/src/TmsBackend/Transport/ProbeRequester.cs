using TmsBackend.Config;
using TmsBackend.Protocol;
using TmsBackend.Transport.Serial;

namespace TmsBackend.Transport;

public interface IProbeRequester
{
    /// <summary>Current readings of every probe on <paramref name="machineIp"/>; empty when the device can't be reached.</summary>
    Task<List<ProbeData>> RequestProbesAsync(string machineIp, string command, TimeSpan timeout, CancellationToken ct = default);
}

/// <summary>
/// Routes a machine_ip to its transport (requestProbes in polling.go): network
/// IPs are dialed fresh on DEFAULT_TCP_PORT, "COMx" paths reuse the persistent
/// connection SerialService opened at startup.
/// </summary>
public sealed class ProbeRequester(
    TcpProbeClient tcp,
    SerialService serial,
    AppOptions options,
    ILogger<ProbeRequester> logger) : IProbeRequester
{
    public async Task<List<ProbeData>> RequestProbesAsync(string machineIp, string command, TimeSpan timeout, CancellationToken ct = default)
    {
        if (SerialService.IsSerialPath(machineIp))
        {
            var conn = serial.GetConnection(machineIp);
            if (conn == null)
            {
                logger.LogWarning("Serial {Path}: no persistent connection tracked (added to master_machine after startup?)", machineIp);
                return [];
            }
            return (await conn.RequestAsync(command, timeout, ct)).Probes;
        }

        return (await tcp.RequestAsync(machineIp, options.DefaultTcpPort, command, timeout, ct)).Probes;
    }
}
