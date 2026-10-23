using MySqlConnector;
using TmsBackend.Data;
using TmsBackend.Models;
using TmsBackend.Protocol;
using TmsBackend.Services;
using TmsBackend.Transport;

namespace TmsBackend.Tests.Services;

internal sealed class ManualTimeProvider(DateTimeOffset start) : TimeProvider
{
    public DateTimeOffset Now { get; set; } = start;

    public override DateTimeOffset GetUtcNow() => Now;

    public void Advance(TimeSpan by) => Now += by;

    /// <summary>A Thai wall-clock moment (UTC+7).</summary>
    public static ManualTimeProvider AtThai(int year, int month, int day, int hour, int minute, int second = 0) =>
        new(new DateTimeOffset(year, month, day, hour, minute, second, TimeSpan.FromHours(7)));
}

internal sealed class FakeTempLogRepository : ITempLogRepository
{
    public List<TempLog> Inserted { get; } = [];
    public Exception? InsertError { get; set; }
    public Dictionary<(string, int), TempLog> Latest { get; } = [];

    public Task InsertAsync(TempLog log, CancellationToken ct = default)
    {
        if (InsertError != null)
            return Task.FromException(InsertError);
        Inserted.Add(log);
        return Task.CompletedTask;
    }

    public List<TempLog> Live { get; } = [];
    public List<TempLog> Archive { get; } = [];
    public List<(TempLogQuery Query, bool FromArchive)> Queries { get; } = [];
    public Exception? QueryError { get; set; }

    public Task<List<TempLog>> QueryAsync(TempLogQuery query, bool fromArchive = false, CancellationToken ct = default)
    {
        Queries.Add((query, fromArchive));
        if (QueryError != null)
            return Task.FromException<List<TempLog>>(QueryError);
        return Task.FromResult((fromArchive ? Archive : Live).ToList());
    }

    public Task<TempLog?> GetLatestAsync(string machineIp, int probeNo, CancellationToken ct = default) =>
        Task.FromResult(Latest.GetValueOrDefault((machineIp, probeNo)));
}

internal sealed class FakeTempErrorRepository : ITempErrorRepository
{
    public List<TempError> Inserted { get; } = [];
    public Exception? InsertError { get; set; }

    public Task InsertAsync(TempError error, CancellationToken ct = default)
    {
        if (InsertError != null)
            return Task.FromException(InsertError);
        Inserted.Add(error);
        return Task.CompletedTask;
    }

    public List<(string? Start, string? End, int Limit)> Queries { get; } = [];

    public Task<List<TempError>> QueryAsync(string? startDate, string? endDate, int limit, CancellationToken ct = default)
    {
        Queries.Add((startDate, endDate, limit));
        return Task.FromResult(Inserted.ToList());
    }
}

internal sealed class FakeProbeRequester : IProbeRequester
{
    public Dictionary<string, List<ProbeData>> Readings { get; } = [];
    public List<(string Ip, TimeSpan Timeout)> Requests { get; } = [];

    public Task<List<ProbeData>> RequestProbesAsync(string machineIp, string command, TimeSpan timeout, CancellationToken ct = default)
    {
        lock (Requests)
            Requests.Add((machineIp, timeout));
        return Task.FromResult(Readings.GetValueOrDefault(machineIp, []));
    }
}

internal sealed class FakeLegacyApi(bool enabled = true) : ILegacyApi
{
    public bool IsEnabled => enabled;
    public List<TempLogPayload> TempLogs { get; } = [];
    public List<AlertPayload> Alerts { get; } = [];

    public Task SendTempLogAsync(TempLogPayload payload, CancellationToken ct = default)
    {
        lock (TempLogs)
            TempLogs.Add(payload);
        return Task.CompletedTask;
    }

    public Task SendTempLogBatchAsync(IReadOnlyList<TempLogPayload> payloads, CancellationToken ct = default) => Task.CompletedTask;

    public Task SendAlertAsync(AlertPayload payload, CancellationToken ct = default)
    {
        lock (Alerts)
            Alerts.Add(payload);
        return Task.CompletedTask;
    }
}

internal sealed class FakeMqtt(bool enabled = true, bool connected = true) : IMqttPublisher
{
    public bool IsEnabled => enabled;
    public bool IsConnected => connected;
    public List<IReadOnlyList<MqttTemperaturePayload>> Published { get; } = [];

    public Task PublishTemperatureBatchAsync(IReadOnlyList<MqttTemperaturePayload> payloads, CancellationToken ct = default)
    {
        lock (Published)
            Published.Add(payloads);
        return Task.CompletedTask;
    }
}

internal sealed class FakeDbPing(bool ok = true) : IDbPing
{
    public bool Ok { get; set; } = ok;
    public Task<bool> PingAsync(CancellationToken ct = default) => Task.FromResult(Ok);
}

internal static class FakeErrors
{
    /// <summary>A real MySqlException with error 1062 (Duplicate entry).</summary>
    public static Exception DuplicateKey()
    {
        var ctor = typeof(MySqlException).GetConstructors(System.Reflection.BindingFlags.NonPublic | System.Reflection.BindingFlags.Instance)
            .First(c => c.GetParameters() is [{ ParameterType: var t0 }, { ParameterType: var t1 }, { ParameterType: var t2 }]
                && t0 == typeof(MySqlErrorCode) && t1 == typeof(string) && t2 == typeof(string));
        return (Exception)ctor.Invoke([MySqlErrorCode.DuplicateKeyEntry, "23000", "Duplicate entry"]);
    }
}
