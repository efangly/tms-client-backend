using System.Collections.Concurrent;
using System.Globalization;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Infrastructure;
using TmsBackend.Models;
using TmsBackend.Protocol;
using TmsBackend.Transport;

namespace TmsBackend.Services;

/// <summary>
/// Background polling (polling.go), three independent loops:
/// <list type="bullet">
/// <item>poll &amp; save every POLL_INTERVAL: read every device, insert temp_log, notify Legacy API and SSE "refresh"</item>
/// <item>alert check every ALERT_INTERVAL: read every device, run the alert state machine, publish MQTT and SSE "temperature"</item>
/// <item>schedule reports every 5 minutes: send a report for probes whose schedule (color field) matches now</item>
/// </list>
/// </summary>
public sealed class PollingService(
    IMachineRepository machines,
    ITempLogRepository tempLogs,
    ITempErrorRepository tempErrors,
    IProbeRequester probes,
    ILegacyApi legacyApi,
    IMqttPublisher mqtt,
    IDbPing db,
    EventHub events,
    AppOptions options,
    AppReadiness readiness,
    TimeProvider time,
    ILogger<PollingService> logger) : BackgroundService
{
    // Out-of-range readings mean a broken probe, not a real measurement.
    private const double MinTempValue = -100.0;
    private const double MaxTempValue = 50.0;
    private const double MinHumiValue = 0.0;
    private const double MaxHumiValue = 100.0;

    private static readonly TimeSpan MachineCacheTtl = TimeSpan.FromMinutes(1);
    private static readonly TimeSpan ScheduleInterval = TimeSpan.FromMinutes(5);
    private static readonly TimeSpan PollRequestTimeout = TimeSpan.FromSeconds(5);
    private static readonly TimeSpan AlertRequestTimeout = TimeSpan.FromSeconds(3);

    private readonly Lock _alertLock = new();
    private readonly Dictionary<string, string> _alertStates = [];
    private readonly Dictionary<string, DateTimeOffset> _alertLastSent = [];

    private readonly Lock _cacheLock = new();
    private List<MasterMachine> _machineCache = [];
    private DateTimeOffset _machineCacheTime;

    private readonly ConcurrentDictionary<Task, byte> _background = new();

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        await readiness.WaitAsync(stoppingToken);
        if (!options.PollingAutostart)
        {
            logger.LogInformation("Polling autostart disabled (POLLING_AUTOSTART=false); use POST /api/poll to poll manually");
            return;
        }

        logger.LogInformation("Starting background polling service...");
        logger.LogInformation("- Poll & Save interval: every {Interval}", options.PollInterval);
        logger.LogInformation("- Alert check interval: every {Interval}", options.AlertInterval);
        logger.LogInformation(legacyApi.IsEnabled
            ? "- Legacy API: ENABLED (POST /legacy/templog, /legacy/templog/alert/notification)"
            : "- Legacy API: DISABLED (LEGACY_API_URL not configured)");
        logger.LogInformation(mqtt.IsEnabled
            ? "- MQTT: ENABLED (publish every alert check)"
            : "- MQTT: DISABLED (MQTT_BROKER not configured)");
        logger.LogInformation("Timezone test: {Now:yyyy-MM-dd HH:mm:ss.fff} (Thailand)", time.ThaiNow());

        logger.LogInformation("Running initial poll and save...");
        await PollAndSaveAsync(stoppingToken);

        await Task.WhenAll(
            RunLoopAsync("poll ticker", options.PollInterval, async ct =>
            {
                await PollAndSaveAsync(ct);
                await CheckMonitoredMachinesAsync(ct);
            }, stoppingToken),
            RunLoopAsync("alert checker", options.AlertInterval, CheckAlertsAsync, stoppingToken),
            RunLoopAsync("schedule report", ScheduleInterval, CheckScheduleReportsAsync, stoppingToken));
    }

    private async Task RunLoopAsync(string name, TimeSpan interval, Func<CancellationToken, Task> tick, CancellationToken ct)
    {
        using var timer = new PeriodicTimer(interval, time);
        try
        {
            while (await timer.WaitForNextTickAsync(ct))
            {
                try
                {
                    await tick(ct);
                }
                catch (Exception e) when (e is not OperationCanceledException || !ct.IsCancellationRequested)
                {
                    // One bad iteration must not kill the loop (Go: recover per tick).
                    logger.LogError(e, "Error in {Loop} iteration (will retry next tick)", name);
                }
            }
        }
        catch (OperationCanceledException) when (ct.IsCancellationRequested)
        {
        }
    }

    /// <summary>Runs one poll-and-save cycle in the background, independent of the regular interval (POST /api/poll).</summary>
    public void TriggerOnce() => RunInBackground(() => PollAndSaveAsync(CancellationToken.None), "manual poll");

    internal async Task PollAndSaveAsync(CancellationToken ct)
    {
        var started = time.GetTimestamp();
        logger.LogInformation("=== Starting Poll & Save cycle ===");

        if (!await db.PingAsync(ct))
        {
            logger.LogWarning("Database ping failed in pollAndSave, will retry next cycle");
            return;
        }

        List<MasterMachine> all;
        try
        {
            all = await machines.GetAllAsync(ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            logger.LogError("pollAndSave - Failed to load machines: {Error}", e.Message);
            return;
        }

        var byIp = GroupByIp(all);
        logger.LogInformation("Found {Ips} unique IPs to poll ({Probes} total probes)", byIp.Count, all.Count);

        int saved = 0, errors = 0;
        var now = time.ThaiNow().TruncateToMicrosecond();
        var sDate = now.ToString("yyyyMMdd", CultureInfo.InvariantCulture);
        var sTime = now.ToString("HH", CultureInfo.InvariantCulture);

        foreach (var (ip, configs) in byIp)
        {
            var received = await probes.RequestProbesAsync(ip, "A", PollRequestTimeout, ct);
            foreach (var reading in received)
            {
                if (IsBrokenSensor(reading))
                {
                    logger.LogWarning("Skipping broken sensor data: {Name} Probe {ProbeNo} (RealValue: 0x{Raw:X4})",
                        configs[0].MachineName, reading.ProbeNo, (ushort)reading.RealValue);
                    continue;
                }

                var (config, hasConfig) = ResolveConfig(configs, reading.ProbeNo);
                if (config.SType == "")
                    config.SType = "t";
                if (IsFormatMismatch(config, hasConfig, reading))
                    continue;

                var value = Adjusted(reading, config);
                if (!IsValidSensorValue(value, config.SType))
                {
                    logger.LogWarning("Skipping out-of-range value: {Name} Probe {ProbeNo} sType={SType} value={Value:F2}",
                        config.MachineName, reading.ProbeNo, config.SType, value);
                    continue;
                }

                var status = StatusFor(value, config);
                var log = new TempLog
                {
                    MachineIP = ip,
                    ProbeNo = reading.ProbeNo,
                    McuID = config.MachineName,
                    TempValue = value,
                    RealValue = reading.RealValue,
                    Status = status,
                    SendTime = now,
                    InsertTime = time.ThaiNow().TruncateToMicrosecond(),
                    SDate = sDate,
                    STime = sTime,
                };

                try
                {
                    await tempLogs.InsertAsync(log, ct);
                    saved++;
                    logger.LogInformation("{Name} Probe {ProbeNo}: {Value:F2}{Unit} [{Type}]",
                        config.MachineName, reading.ProbeNo, value, config.GetUnit(), config.GetTypeLabel());

                    if (legacyApi.IsEnabled)
                    {
                        var payload = new TempLogPayload(config.MachineName, "00000110", value, reading.RealValue, sDate, sTime);
                        var name = config.MachineName;
                        var probeNo = reading.ProbeNo;
                        RunInBackground(() => legacyApi.SendTempLogAsync(payload),
                            $"pollAndSave - Failed to send to Legacy API (machine={name}, probe={probeNo})");
                    }
                }
                catch (Exception e) when (Db.IsDuplicateKey(e))
                {
                    logger.LogInformation("Duplicate log entry skipped for {Name} Probe {ProbeNo}", config.MachineName, reading.ProbeNo);
                }
                catch (Exception e) when (e is not OperationCanceledException)
                {
                    logger.LogError("pollAndSave - Failed to save temp log (machine={Name}, probe={ProbeNo}): {Error}",
                        config.MachineName, reading.ProbeNo, e.Message);
                    errors++;
                }

                await CheckProbeAlertAsync(config, reading.ProbeNo, value, ct);
            }
        }

        // Warm the cache so the alert loop can skip its own DB query for a minute.
        lock (_cacheLock)
        {
            _machineCache = all;
            _machineCacheTime = time.GetUtcNow();
        }

        logger.LogInformation("=== Poll & Save completed in {Elapsed} === Saved: {Saved} logs, {Errors} errors",
            time.GetElapsedTime(started), saved, errors);
        events.Publish(new DataSavedEvent(saved, errors));
    }

    internal async Task CheckAlertsAsync(CancellationToken ct)
    {
        if (!await db.PingAsync(ct))
        {
            logger.LogWarning("Database ping failed in checkAlerts");
            return;
        }

        List<MasterMachine> all;
        try
        {
            all = await GetMachinesAsync(ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            logger.LogWarning("checkAlerts - Failed to load machines: {Error}", e.Message);
            return;
        }

        var payloads = new List<MqttTemperaturePayload>();
        var now = time.ThaiNow();
        var timestamp = now.ToString("yyyy-MM-dd HH:mm:ss", CultureInfo.InvariantCulture);

        foreach (var (ip, configs) in GroupByIp(all))
        {
            var received = await probes.RequestProbesAsync(ip, "A", AlertRequestTimeout, ct);
            foreach (var reading in received)
            {
                if (IsBrokenSensor(reading))
                    continue;

                var (config, hasConfig) = ResolveConfig(configs, reading.ProbeNo);
                if (IsFormatMismatch(config, hasConfig, reading))
                    continue;

                var value = Adjusted(reading, config);
                if (!IsValidSensorValue(value, config.SType))
                {
                    logger.LogWarning("Skipping out-of-range value: {Name} Probe {ProbeNo} sType={SType} value={Value:F2}",
                        config.MachineName, reading.ProbeNo, config.SType, value);
                    continue;
                }

                await CheckProbeAlertAsync(config, reading.ProbeNo, value, ct);

                payloads.Add(new MqttTemperaturePayload(
                    config.MachineName, value, StatusFor(value, config), config.SType, timestamp,
                    config.GetMinTemp(), config.GetMaxTemp(), config.MachineIP, reading.ProbeNo));
            }
        }

        if (payloads.Count == 0)
            return;

        if (mqtt.IsEnabled)
        {
            if (!mqtt.IsConnected)
                logger.LogInformation("MQTT not connected - skipping publish");
            else
                RunInBackground(() => mqtt.PublishTemperatureBatchAsync(payloads), "MQTT batch publish failed");
        }

        events.Publish(payloads.Select(p => new TemperatureUpdateEvent(
            p.Probe, p.Temp, p.Status, p.MachineType, p.Timestamp, p.MinTemp, p.MaxTemp, p.IPAddress, p.ProbeNo)).ToList());
    }

    /// <summary>
    /// Alert state machine per probe (checkProbeAlert): N→H/L writes temp_error
    /// and sends an alert; H/L→N sends a recovery. With chkReport=1 an ongoing
    /// H/L repeats, at most once per poll interval, until it returns to normal.
    /// </summary>
    internal async Task CheckProbeAlertAsync(MasterMachine machine, int probeNo, double temp, CancellationToken ct)
    {
        var key = $"{machine.MachineIP}:{probeNo}";
        var min = machine.GetMinTemp();
        var max = machine.GetMaxTemp();
        var state = temp < min ? "L" : temp > max ? "H" : "N";
        var repeatWhileOutOfRange = machine.ChkReport == "1" && state is "H" or "L";

        string prevState;
        bool isTransition;
        // Claim the transition atomically so the poll and alert loops never both notify it.
        lock (_alertLock)
        {
            prevState = _alertStates.GetValueOrDefault(key, "");
            isTransition = state != prevState;
            if (!isTransition && !repeatWhileOutOfRange)
                return;
            _alertStates[key] = state;

            // Ongoing out-of-range: resend once per poll interval, not every alert tick.
            if (!isTransition && _alertLastSent.TryGetValue(key, out var last) && time.GetUtcNow() - last < options.PollInterval)
                return;
        }

        var now = time.ThaiNow().TruncateToMicrosecond();
        var dateStr = now.ToString("yyyyMMdd", CultureInfo.InvariantCulture);
        var timeStr = now.ToString("HH:mm:ss", CultureInfo.InvariantCulture);
        var displayDate = now.ToString("yyyy/MM/dd", CultureInfo.InvariantCulture);
        var unit = machine.GetUnit();
        var label = machine.GetTypeLabel();
        var range = Invariant($"{min:F2}-{max:F2}{unit}");

        if (state is "H" or "L")
        {
            var message = Invariant($"{label} {(state == "H" ? "สูง" : "ต่ำ")}เกิน (ค่าปัจจุบัน: {temp:F2}{unit}, ช่วง: {range}) {machine.MachineName} {displayDate} {timeStr}");
            logger.LogWarning("ALERT: {Name} Probe {ProbeNo} - {Label} {Temp:F2}{Unit} is {Type} (min: {Min:F2}, max: {Max:F2})",
                machine.MachineName, probeNo, label, temp, unit, state == "H" ? "HIGH" : "LOW", min, max);

            try
            {
                await tempErrors.InsertAsync(new TempError
                {
                    MachineIP = machine.MachineIP,
                    ProbeNo = probeNo,
                    MachineName = machine.MachineName,
                    TempValue = temp,
                    ErrorTime = time.ThaiNow().TruncateToMicrosecond(),
                    MinTemp = min,
                    MaxTemp = max,
                    TempStatus = "p",
                    ErrorType = "o",
                    SType = machine.SType,
                }, ct);
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                if (!Db.IsDuplicateKey(e))
                    logger.LogError("checkAlerts - Failed to create temp_error: {Error}", e.Message);
            }

            SendAlert(new AlertPayload(machine.MachineName, state == "H" ? "00000010" : "00000011", temp, (int)(temp * 100),
                dateStr, timeStr, message, state == "H" ? "high" : "low", machine.MachineName, probeNo, min, max));

            if (machine.ChkReport == "1" && machine.ChkMon != "1")
                await UpdateChkMonAsync(machine.MachineIP, probeNo, "1", ct);

            if (repeatWhileOutOfRange)
            {
                lock (_alertLock)
                    _alertLastSent[key] = time.GetUtcNow();
            }
        }

        if (state == "N" && prevState is "H" or "L")
        {
            var message = Invariant($"{label} กลับเข้าช่วงปกติแล้ว (ค่าปัจจุบัน: {temp:F2}{unit}, ช่วง: {range}) {machine.MachineName} {displayDate} {timeStr}");
            logger.LogInformation("NORMAL: {Name} Probe {ProbeNo} - {Temp:F2}{Unit} returned to normal range", machine.MachineName, probeNo, temp, unit);

            SendAlert(new AlertPayload(machine.MachineName, "00000001", temp, (int)(temp * 100),
                dateStr, timeStr, message, "normal", machine.MachineName, probeNo, min, max));

            if (machine.ChkReport == "1")
            {
                await UpdateChkMonAsync(machine.MachineIP, probeNo, "0", ct);
                lock (_alertLock)
                    _alertLastSent.Remove(key);
            }
        }
    }

    /// <summary>Re-runs the alert check while any probe is flagged chkMon=1 (actively alerting with chkReport on).</summary>
    internal async Task CheckMonitoredMachinesAsync(CancellationToken ct)
    {
        List<MasterMachine> all;
        try
        {
            all = await GetMachinesAsync(ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            logger.LogWarning("checkMonitoredMachines - Failed to load machines: {Error}", e.Message);
            return;
        }
        if (all.Any(m => m.ChkMon == "1"))
            await CheckAlertsAsync(ct);
    }

    /// <summary>Sends a report for every probe whose schedule (color field, "HHmm" list) contains the current 5-minute slot.</summary>
    internal async Task CheckScheduleReportsAsync(CancellationToken ct)
    {
        if (!await db.PingAsync(ct))
        {
            logger.LogWarning("Database ping failed in checkScheduleReports");
            return;
        }

        var now = time.ThaiNow();
        var slot = new DateTime(now.Ticks - now.Ticks % ScheduleInterval.Ticks, now.Kind);
        var currentHHmm = slot.ToString("HHmm", CultureInfo.InvariantCulture);

        List<MasterMachine>? all = null;
        lock (_cacheLock)
        {
            if (time.GetUtcNow() - _machineCacheTime < MachineCacheTtl && _machineCache.Count > 0)
                all = _machineCache;
        }
        if (all == null)
        {
            try
            {
                all = await machines.GetAllAsync(ct);
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                logger.LogError("checkScheduleReports - Failed to load machines: {Error}", e.Message);
                return;
            }
        }

        foreach (var machine in all.Where(m => m.HasScheduleTime(currentHHmm)))
        {
            TempLog? latest;
            try
            {
                latest = await tempLogs.GetLatestAsync(machine.MachineIP, machine.ProbeNo, ct);
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                logger.LogWarning("checkScheduleReports - query failed for {Name} Probe {ProbeNo}: {Error}", machine.MachineName, machine.ProbeNo, e.Message);
                continue;
            }
            if (latest?.TempValue is not { } temp)
            {
                logger.LogInformation("checkScheduleReports - skipping {Name} Probe {ProbeNo}: no recent data", machine.MachineName, machine.ProbeNo);
                continue;
            }

            var unit = machine.GetUnit();
            var timeStr = now.ToString("HH:mm:ss", CultureInfo.InvariantCulture);
            var message = Invariant($"[รายงาน] {machine.GetTypeLabel()} {machine.MachineName}({machine.ProbeNo}) ค่าปัจจุบัน: {temp:F2}{unit} ช่วง: {machine.GetMinTemp():F2}-{machine.GetMaxTemp():F2}{unit} {now:yyyy/MM/dd} {timeStr}");
            logger.LogInformation("Schedule report: {Name} Probe {ProbeNo} at {Slot}", machine.MachineName, machine.ProbeNo, currentHHmm);

            SendAlert(new AlertPayload(machine.MachineName, "00000110", temp, (int)(temp * 100),
                now.ToString("yyyyMMdd", CultureInfo.InvariantCulture), timeStr, message, "report",
                machine.MachineName, machine.ProbeNo, machine.GetMinTemp(), machine.GetMaxTemp()));
        }
    }

    /// <summary>
    /// Machines from a 1-minute cache, falling back to the DB; on a DB error a
    /// stale cache is served rather than failing. The DB call is made outside the lock.
    /// </summary>
    internal async Task<List<MasterMachine>> GetMachinesAsync(CancellationToken ct)
    {
        List<MasterMachine> stale;
        lock (_cacheLock)
        {
            if (time.GetUtcNow() - _machineCacheTime < MachineCacheTtl && _machineCache.Count > 0)
                return _machineCache;
            stale = _machineCache;
        }

        List<MasterMachine> fresh;
        try
        {
            fresh = await machines.GetAllAsync(ct);
        }
        catch (Exception e) when (e is not OperationCanceledException && stale.Count > 0)
        {
            logger.LogWarning("getMachines - DB error, serving stale cache: {Error}", e.Message);
            return stale;
        }

        lock (_cacheLock)
        {
            _machineCache = fresh;
            _machineCacheTime = time.GetUtcNow();
        }
        return fresh;
    }

    private async Task UpdateChkMonAsync(string machineIp, int probeNo, string value, CancellationToken ct)
    {
        try
        {
            await machines.UpdateChkMonAsync(machineIp, probeNo, value, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            logger.LogError("updateChkMon - Failed to update chkMon (machine={Ip}, probe={ProbeNo}): {Error}", machineIp, probeNo, e.Message);
        }
    }

    private void SendAlert(AlertPayload payload)
    {
        if (!legacyApi.IsEnabled)
            return;
        RunInBackground(async () =>
        {
            await legacyApi.SendAlertAsync(payload);
            logger.LogInformation("Alert notification sent for {Name} Probe {ProbeNo} [{Type}]", payload.MachineName, payload.ProbeNo, payload.AlertType);
        }, $"sendAlertNotification - Failed to send alert (machine={payload.MachineName}, probe={payload.ProbeNo}, type={payload.AlertType})");
    }

    /// <summary>Fire-and-forget side effect (Go: `go func()`), logging failures.</summary>
    private void RunInBackground(Func<Task> work, string errorContext)
    {
        var task = Task.Run(async () =>
        {
            try
            {
                await work();
            }
            catch (Exception e)
            {
                logger.LogError("{Context}: {Error}", errorContext, e.Message);
            }
        });
        _background.TryAdd(task, 0);
        task.ContinueWith(t => _background.TryRemove(t, out _), TaskScheduler.Default);
    }

    /// <summary>Test hook: waits for fire-and-forget notifications to finish.</summary>
    internal Task DrainBackgroundAsync() => Task.WhenAll(_background.Keys);

    internal string? AlertState(string key)
    {
        lock (_alertLock)
            return _alertStates.GetValueOrDefault(key);
    }

    internal void SetAlertState(string key, string state)
    {
        lock (_alertLock)
            _alertStates[key] = state;
    }

    internal void SeedMachineCache(List<MasterMachine> cache, DateTimeOffset at)
    {
        lock (_cacheLock)
        {
            _machineCache = cache;
            _machineCacheTime = at;
        }
    }

    /// <summary>Probes grouped by machine_ip, in first-seen order.</summary>
    private static List<(string Ip, List<MasterMachine> Configs)> GroupByIp(List<MasterMachine> all) =>
        all.GroupBy(m => m.MachineIP).Select(g => (g.Key, g.ToList())).ToList();

    private static bool IsBrokenSensor(ProbeData p) => p.RealValue is 65535 or -1;

    /// <summary>The probe's own config, or a copy of the device's first probe when the device reports an unconfigured probe.</summary>
    private static (MasterMachine Config, bool HasConfig) ResolveConfig(List<MasterMachine> configs, int probeNo)
    {
        var own = configs.FirstOrDefault(c => c.ProbeNo == probeNo);
        if (own != null)
            return (own.Clone(), true);
        var borrowed = configs[0].Clone();
        borrowed.ProbeNo = probeNo;
        return (borrowed, false);
    }

    /// <summary>A humidity reading for a probe configured as temperature (or vice versa) is a stale/legacy-format response.</summary>
    private bool IsFormatMismatch(MasterMachine config, bool hasConfig, ProbeData reading)
    {
        if (!hasConfig || config.IsHumidityType() == (reading.McuID == "h"))
            return false;
        logger.LogWarning("Skipping mismatched probe format: {Name} Probe {ProbeNo} configured as sType={SType} but device sent McuID={McuID} (likely a stale/legacy-format response for this probe)",
            config.MachineName, reading.ProbeNo, config.SType, reading.McuID);
        return true;
    }

    private static double Adjusted(ProbeData reading, MasterMachine config) =>
        HexProtocolParser.RoundTo2Decimal(reading.TempValue + config.GetAdjTemp());

    private static string StatusFor(double value, MasterMachine config) =>
        value < config.GetMinTemp() ? "L" : value > config.GetMaxTemp() ? "H" : "N";

    internal static bool IsValidSensorValue(double value, string sType) => sType.ToLowerInvariant() == "h"
        ? value is >= MinHumiValue and <= MaxHumiValue
        : value is >= MinTempValue and <= MaxTempValue;

    private static string Invariant(FormattableString s) => s.ToString(CultureInfo.InvariantCulture);
}
