namespace TmsBackend.Protocol;

/// <summary>Result of one request to a probe device over TCP or serial (tcpclient.ServerResponse / serialclient.Response).</summary>
public sealed class ProbeResponse
{
    /// <summary>IP address or COM path.</summary>
    public string Source { get; init; } = "";
    public bool Connected { get; set; }
    /// <summary>Raw frame as lowercase hex (Go hex.EncodeToString).</summary>
    public string Data { get; set; } = "";
    public string Error { get; set; } = "";
    public DateTime Timestamp { get; init; } = DateTime.Now;
    public List<ProbeData> Probes { get; set; } = [];

    public const int MaxRequestRetries = 3;

    /// <summary>
    /// Shared request loop: send, parse, drop implausible readings, and retry
    /// (up to 3 attempts) while a reading is anomalous. A transport error ends
    /// the request immediately.
    /// </summary>
    public static async Task<ProbeResponse> RequestWithRetryAsync(
        string source,
        Func<CancellationToken, Task<(byte[] Frame, bool Connected)>> sendOnce,
        ILogger logger,
        CancellationToken ct)
    {
        var result = new ProbeResponse { Source = source };
        byte[] frame = [];
        var probes = new List<ProbeData>();

        for (var attempt = 1; attempt <= MaxRequestRetries; attempt++)
        {
            try
            {
                (frame, var connected) = await sendOnce(ct);
                result.Connected |= connected;
            }
            catch (ProbeTransportException e)
            {
                result.Connected |= e.Connected;
                result.Error = e.Message;
                logger.LogWarning("{Source}: {Error}", source, e.Message);
                return result;
            }

            if (frame.Length == 0)
                break;

            var parsed = HexProtocolParser.Parse(frame, source, logger);
            var (valid, hasAnomaly) = HexProtocolParser.FilterValidProbes(parsed, source, logger);
            probes = valid;
            if (!hasAnomaly || attempt == MaxRequestRetries)
                break;

            logger.LogWarning("{Source}: anomalous sensor data detected, retrying request (attempt {Attempt}/{Max})", source, attempt, MaxRequestRetries);
        }

        if (frame.Length > 0)
        {
            result.Data = Convert.ToHexStringLower(frame);
            result.Probes = probes;
            logger.LogInformation("{Source}: parsed {Count} probes", source, probes.Count);
        }
        return result;
    }
}

/// <summary>A connect/write/read failure. <see cref="Connected"/> tells whether the link was up when it failed.</summary>
public sealed class ProbeTransportException(string message, bool connected, Exception? inner = null) : Exception(message, inner)
{
    public bool Connected { get; } = connected;
}
