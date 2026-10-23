using System.Globalization;
using System.Text.Json;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Infrastructure;
using TmsBackend.Json;
using TmsBackend.Models;
using TmsBackend.Services;

namespace TmsBackend.Endpoints;

/// <summary>
/// The HTTP API, route for route as internal/handlers/routes.go. Response
/// bodies mirror the Go handlers, including fiber.Map's alphabetical key
/// order and nil slices serialized as null.
/// </summary>
public static class ApiEndpoints
{
    private const int DefaultLimit = 100;

    // encoding/json matches object keys case-insensitively.
    private static readonly JsonSerializerOptions RequestJson = new(GoJson.Options) { PropertyNameCaseInsensitive = true };

    public static void MapTmsApi(this IEndpointRouteBuilder app)
    {
        app.MapGet("/health", Health);

        var api = app.MapGroup("/api");

        api.MapGet("/devices", GetDevices);
        api.MapGet("/devices/{id}", GetDevice);
        api.MapPost("/devices", CreateDevice);
        api.MapPut("/devices/{id}", UpdateDevice);
        api.MapDelete("/devices/{id}", DeleteDevice);

        // Legacy-compatible machine routes
        api.MapGet("/machines", GetMachines);
        api.MapPut("/machines/{machineIp}/{probeNo}", UpdateMachine);

        // Schedule (stored in master_machine.color)
        api.MapGet("/machines/{machineIp}/{probeNo}/schedule", GetSchedule);
        api.MapPut("/machines/{machineIp}/{probeNo}/schedule", SetSchedule);
        api.MapPost("/machines/{machineIp}/{probeNo}/schedule/{time}", AddScheduleTime);
        api.MapDelete("/machines/{machineIp}/{probeNo}/schedule/{time}", RemoveScheduleTime);

        api.MapGet("/temp-logs", GetTempLogs);
        api.MapGet("/reports/templog", GetTempLogReport);
        api.MapGet("/temp-errors", GetTempErrors);

        api.MapPost("/archive/run", RunArchive);
        api.MapGet("/archive", GetArchiveManifests);
        api.MapPost("/archive/restore", RestoreArchive);

        api.MapPost("/poll", TriggerPoll);

        api.MapGet("/temperature-stream", TemperatureStream.HandleAsync);
    }

    // ── Health ───────────────────────────────────────────────────────────────

    private static async Task<IResult> Health(IDbPing db, IMqttPublisher mqtt, CancellationToken ct)
    {
        var dbOk = await db.PingAsync(ct);
        return Results.Json(new { db = dbOk, mqtt = mqtt.IsConnected, status = dbOk ? "ok" : "degraded", version = AppVersion.Value },
            statusCode: dbOk ? 200 : 503);
    }

    // ── Devices ──────────────────────────────────────────────────────────────

    private static async Task<IResult> GetDevices(IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        try
        {
            return Results.Json(await machines.GetAllAsync(ct));
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"GetDevices failed: {e.Message}");
        }
    }

    private static async Task<IResult> GetDevice(string id, HttpRequest req, IMachineRepository machines, CancellationToken ct)
    {
        var probeNo = QueryInt(req, "probeNo", 1);
        MasterMachine? machine = null;
        try
        {
            machine = await machines.GetAsync(id, probeNo, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            // Go treats any lookup error as not found.
        }
        return machine == null ? Error(404, "Machine not found") : Results.Json(machine);
    }

    private static async Task<IResult> CreateDevice(HttpRequest req, IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        var (machine, bad) = await ReadBodyAsync<MasterMachine>(req, ct);
        if (bad != null)
        {
            log.LogError("CreateDevice - Failed to parse body: {Error}", bad);
            return Error(400, bad);
        }

        if (machine!.ProbeNo == 0)
            machine.ProbeNo = 1;
        if (machine.SType == "")
            machine.SType = "t";

        try
        {
            await machines.CreateAsync(machine, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"CreateDevice - Failed to create machine: {e.Message}");
        }
        return Results.Json(machine, statusCode: 201);
    }

    private static async Task<IResult> UpdateDevice(string id, HttpRequest req, IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        var probeNo = QueryInt(req, "probeNo", 0);
        MasterMachine? machine = null;
        try
        {
            machine = probeNo > 0 ? await machines.GetAsync(id, probeNo, ct) : await machines.GetFirstByIpAsync(id, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            log.LogError("UpdateDevice - lookup failed (ip={Ip}, probe={ProbeNo}): {Error}", id, probeNo, e.Message);
        }
        if (machine == null)
        {
            log.LogError("UpdateDevice - Machine not found (ip={Ip}, probe={ProbeNo})", id, probeNo);
            return Error(404, "Machine not found");
        }
        return await ApplyUpdateAsync("UpdateDevice", machine, req, machines, log, ct);
    }

    private static async Task<IResult> DeleteDevice(string id, HttpRequest req, IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        var probeNo = QueryInt(req, "probeNo", 0);
        try
        {
            await machines.DeleteAsync(id, probeNo > 0 ? probeNo : null, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"DeleteDevice - Failed to delete machine (ip={id}, probe={probeNo}): {e.Message}");
        }
        return Results.Json(new { success = true });
    }

    // ── Machines ─────────────────────────────────────────────────────────────

    private static async Task<IResult> GetMachines(IMachineRepository machines, TimeProvider time, ILogger<Api> log, CancellationToken ct)
    {
        List<MasterMachine> all;
        try
        {
            all = await machines.GetAllAsync(ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"GetMachines failed: {e.Message}");
        }

        // Latest reading per probe in one query; a failure just leaves everything Offline (as in Go).
        var latest = new Dictionary<(string, int), LatestReading>();
        try
        {
            foreach (var r in await machines.GetLatestReadingsAsync(ct))
                latest[(r.MachineIP, r.ProbeNo)] = r;
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            log.LogWarning("GetMachines - latest readings query failed: {Error}", e.Message);
        }

        var now = time.GetLocalNow().DateTime;
        var result = all.Select(m =>
        {
            var w = MachineWithStatus.From(m);
            w.OnlineStatus = "Offline";
            if (latest.TryGetValue((m.MachineIP, m.ProbeNo), out var r))
            {
                w.CurrentValue = r.TempValue;
                w.LastUpdate = r.InsertTime.ToString("yyyy-MM-dd HH:mm:ss", CultureInfo.InvariantCulture);
                if (now - r.InsertTime < TimeSpan.FromMinutes(10))
                    w.OnlineStatus = "Online";
            }
            return w;
        }).ToList();
        return Results.Json(result);
    }

    private static async Task<IResult> UpdateMachine(string machineIp, string probeNo, HttpRequest req, IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        // Go ignores the Atoi error: a non-numeric probeNo looks up probe 0.
        int.TryParse(probeNo, NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture, out var probe);
        MasterMachine? machine = null;
        try
        {
            machine = await machines.GetAsync(machineIp, probe, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            log.LogError("UpdateMachine - lookup failed (ip={Ip}, probe={ProbeNo}): {Error}", machineIp, probe, e.Message);
        }
        if (machine == null)
        {
            log.LogError("UpdateMachine - Machine not found (ip={Ip}, probe={ProbeNo})", machineIp, probe);
            return Error(404, "Machine not found");
        }
        return await ApplyUpdateAsync("UpdateMachine", machine, req, machines, log, ct);
    }

    /// <summary>Partial update from a JSON object (primary keys ignored), then re-read and return the row.</summary>
    private static async Task<IResult> ApplyUpdateAsync(string op, MasterMachine machine, HttpRequest req, IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        var (body, bad) = await ReadBodyAsync<Dictionary<string, JsonElement>>(req, ct);
        if (bad != null)
        {
            log.LogError("{Op} - Failed to parse body: {Error}", op, bad);
            return Error(400, bad);
        }

        try
        {
            await machines.UpdateAsync(machine.MachineIP, machine.ProbeNo, MachineUpdate.ToColumns(body!), ct);
            var updated = await machines.GetAsync(machine.MachineIP, machine.ProbeNo, ct)
                ?? throw new InvalidOperationException("record not found after update");
            return Results.Json(updated);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"{op} - Failed to update machine (ip={machine.MachineIP}, probe={machine.ProbeNo}): {e.Message}");
        }
    }

    // ── Schedule ─────────────────────────────────────────────────────────────

    private static async Task<MasterMachine?> FindScheduleMachineAsync(IMachineRepository machines, string machineIp, string probeNo, CancellationToken ct)
    {
        if (!int.TryParse(probeNo, NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture, out var probe))
            return null;
        try
        {
            return await machines.GetAsync(machineIp, probe, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return null;
        }
    }

    /// <summary>{"times": [...]}; an empty schedule is null, like Go's nil slice.</summary>
    private static IResult Times(MasterMachine m)
    {
        var times = m.GetScheduleTimes();
        return Results.Json(new { times = times.Count == 0 ? null : times });
    }

    /// <summary>HHmm with minutes on a 5-minute boundary (isValidHHmm).</summary>
    internal static bool IsValidHHmm(string s) =>
        s.Length == 4
        && int.TryParse(s.AsSpan(0, 2), NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture, out var h)
        && int.TryParse(s.AsSpan(2), NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture, out var m)
        && h is >= 0 and <= 23 && m is >= 0 and <= 59 && m % 5 == 0;

    private static async Task<IResult> GetSchedule(string machineIp, string probeNo, IMachineRepository machines, CancellationToken ct)
    {
        var machine = await FindScheduleMachineAsync(machines, machineIp, probeNo, ct);
        return machine == null ? Error(404, "Machine not found") : Times(machine);
    }

    private sealed class ScheduleBody
    {
        public List<string>? Times { get; set; }
    }

    private static async Task<IResult> SetSchedule(string machineIp, string probeNo, HttpRequest req, IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        var machine = await FindScheduleMachineAsync(machines, machineIp, probeNo, ct);
        if (machine == null)
            return Error(404, "Machine not found");

        var (body, bad) = await ReadBodyAsync<ScheduleBody>(req, ct);
        if (bad != null)
            return Error(400, bad);

        var validated = new List<string>();
        foreach (var raw in body!.Times ?? [])
        {
            var t = raw.Trim();
            if (!IsValidHHmm(t))
                return Error(400, $"invalid time format: {t} (expected HHmm, e.g. 0800)");
            if (!validated.Contains(t))
                validated.Add(t);
        }
        if (validated.Count > 6)
            return Error(400, "maximum 6 schedule times allowed");

        return await SaveScheduleAsync("SetSchedule", machine, validated, machines, log, ct);
    }

    private static async Task<IResult> AddScheduleTime(string machineIp, string probeNo, string time, IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        var machine = await FindScheduleMachineAsync(machines, machineIp, probeNo, ct);
        if (machine == null)
            return Error(404, "Machine not found");

        var t = time.Trim();
        if (!IsValidHHmm(t))
            return Error(400, $"invalid time format: {t} (expected HHmm, e.g. 0800)");

        var times = machine.GetScheduleTimes();
        if (times.Contains(t))
            return Times(machine);
        if (times.Count >= 6)
            return Error(400, "maximum 6 schedule times allowed");
        times.Add(t);

        return await SaveScheduleAsync("AddScheduleTime", machine, times, machines, log, ct);
    }

    private static async Task<IResult> RemoveScheduleTime(string machineIp, string probeNo, string time, IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        var machine = await FindScheduleMachineAsync(machines, machineIp, probeNo, ct);
        if (machine == null)
            return Error(404, "Machine not found");

        var t = time.Trim();
        var remaining = machine.GetScheduleTimes().Where(x => x != t).ToList();
        return await SaveScheduleAsync("RemoveScheduleTime", machine, remaining, machines, log, ct);
    }

    private static async Task<IResult> SaveScheduleAsync(string op, MasterMachine machine, List<string> times, IMachineRepository machines, ILogger<Api> log, CancellationToken ct)
    {
        var color = string.Join(",", times);
        try
        {
            await machines.UpdateColorAsync(machine.MachineIP, machine.ProbeNo, color, ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"{op} - Failed to update (ip={machine.MachineIP}, probe={machine.ProbeNo}): {e.Message}");
        }
        machine.Color = color;
        return Times(machine);
    }

    // ── Temperature logs ─────────────────────────────────────────────────────

    private static async Task<IResult> GetTempLogs(HttpRequest req, IMachineRepository machines, ITempLogRepository logs, ArchiveService archive, ILogger<Api> log, CancellationToken ct)
    {
        var startDate = Query(req, "startDate");
        var endDate = Query(req, "endDate");
        var limit = QueryInt(req, "limit", DefaultLimit);
        var devices = Query(req, "devices");
        var includeArchive = QueryBool(req, "includeArchive", false);

        var all = devices != "" ? await LoadMachinesForFilterAsync(machines, "GetTempLogs", log, ct) : [];
        var filter = devices != "" ? DeviceFilter.Parse(devices, all) : null;

        List<TempLog> rows;
        try
        {
            rows = await logs.QueryAsync(new TempLogQuery { StartDate = startDate, EndDate = endDate, Devices = filter, Descending = true, Limit = limit }, ct: ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"GetTempLogs failed: {e.Message}");
        }

        if (includeArchive && startDate != "" && endDate != "")
        {
            try
            {
                var archived = await FetchArchivedAsync(startDate, endDate, filter, logs, archive, ct);
                rows = Merge(rows, archived, descending: true, limit);
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                log.LogError("GetTempLogs - archive fetch failed: {Error}", e.Message);
            }
        }
        return Results.Json(rows);
    }

    private static async Task<IResult> GetTempLogReport(HttpRequest req, IMachineRepository machines, ITempLogRepository logs, ArchiveService archive, TimeProvider time, ILogger<Api> log, CancellationToken ct)
    {
        var startDate = Query(req, "startDate");
        var endDate = Query(req, "endDate");
        var devices = Query(req, "devices");

        if (startDate == "" || endDate == "")
            return Error(400, "startDate and endDate are required");
        if (!IsDate(startDate))
            return Error(400, "invalid startDate format, use YYYY-MM-DD");
        if (!IsDate(endDate))
            return Error(400, "invalid endDate format, use YYYY-MM-DD");

        // Archived data is pulled in automatically once the range reaches back past the retention window.
        var cutoff = time.ThaiNow().AddDays(-archive.RetentionDays).ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);
        var needsArchive = string.CompareOrdinal(startDate, cutoff) < 0;

        // Names fall back to the IP when machines can't be loaded.
        var all = await LoadMachinesForFilterAsync(machines, "GetTempLogReport", log, ct);
        var names = new Dictionary<(string, int), string>();
        foreach (var m in all)
            names[(m.MachineIP, m.ProbeNo)] = m.MachineName;
        var filter = devices != "" ? DeviceFilter.Parse(devices, all) : null;

        List<TempLog> rows;
        try
        {
            rows = await logs.QueryAsync(new TempLogQuery { StartDate = startDate, EndDate = endDate, Devices = filter }, ct: ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"GetTempLogReport failed: {e.Message}");
        }

        if (needsArchive)
        {
            try
            {
                rows = Merge(rows, await FetchArchivedAsync(startDate, endDate, filter, logs, archive, ct), descending: false, 0);
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                log.LogError("GetTempLogReport - archive fetch failed: {Error}", e.Message);
            }
        }

        // One chart series per probe, labelled "<machine name>-P<probeNo>".
        var series = new List<Series>();
        var byLabel = new Dictionary<string, Series>();
        foreach (var row in rows)
        {
            var name = names.GetValueOrDefault((row.MachineIP, row.ProbeNo), "");
            if (name == "")
                name = row.MachineIP;
            var label = $"{name}-P{row.ProbeNo}";
            if (!byLabel.TryGetValue(label, out var s))
            {
                s = new Series(label, []);
                byLabel[label] = s;
                series.Add(s);
            }
            if (row.TempValue is { } v)
                s.data.Add(new SeriesPoint(GoTimeConverter.Format(row.InsertTime), v));
        }

        return Results.Json(new { data = rows, series = series.Count == 0 ? null : series });
    }

    private sealed record SeriesPoint(string x, double y);

    private sealed record Series(string label, List<SeriesPoint> data);

    private static async Task<IResult> GetTempErrors(HttpRequest req, ITempErrorRepository errors, ILogger<Api> log, CancellationToken ct)
    {
        try
        {
            return Results.Json(await errors.QueryAsync(Query(req, "startDate"), Query(req, "endDate"), QueryInt(req, "limit", DefaultLimit), ct));
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"GetTempErrors failed: {e.Message}");
        }
    }

    /// <summary>Restores [start, end] from archive files, then reads temp_log_archive with the same device filter.</summary>
    private static async Task<List<TempLog>> FetchArchivedAsync(string startDate, string endDate, DeviceFilter? filter, ITempLogRepository logs, ArchiveService archive, CancellationToken ct)
    {
        await archive.RestoreRangeAsync(startDate, endDate, ct);
        return await logs.QueryAsync(new TempLogQuery { StartDate = startDate, EndDate = endDate, Devices = filter }, fromArchive: true, ct);
    }

    /// <summary>Live + archived rows sorted by insert time, cut to <paramref name="limit"/> when positive.</summary>
    internal static List<TempLog> Merge(List<TempLog> live, List<TempLog> archived, bool descending, int limit)
    {
        var all = live.Concat(archived);
        var sorted = (descending ? all.OrderByDescending(l => l.InsertTime) : all.OrderBy(l => l.InsertTime)).ToList();
        return limit > 0 && sorted.Count > limit ? sorted[..limit] : sorted;
    }

    private static async Task<List<MasterMachine>> LoadMachinesForFilterAsync(IMachineRepository machines, string op, ILogger log, CancellationToken ct)
    {
        try
        {
            return await machines.GetAllAsync(ct);
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            log.LogError("{Op} - Failed to load machines: {Error}", op, e.Message);
            return [];
        }
    }

    // ── Archive & polling control ────────────────────────────────────────────

    private static async Task<IResult> RunArchive(ArchiveService archive, ILogger<Api> log, CancellationToken ct)
    {
        try
        {
            return Results.Json(await archive.RunArchiveAsync(ct));
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"RunArchiveHandler failed: {e.Message}");
        }
    }

    private static async Task<IResult> GetArchiveManifests(HttpRequest req, IArchiveRepository repo, ILogger<Api> log, CancellationToken ct)
    {
        try
        {
            return Results.Json(await repo.ListManifestsAsync(Query(req, "startDate"), Query(req, "endDate"), ct));
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"GetArchiveManifests failed: {e.Message}");
        }
    }

    private static async Task<IResult> RestoreArchive(HttpRequest req, ArchiveService archive, ILogger<Api> log, CancellationToken ct)
    {
        var startDate = Query(req, "startDate");
        var endDate = Query(req, "endDate");
        if (startDate == "" || endDate == "")
            return Error(400, "startDate and endDate are required");
        if (!IsDate(startDate))
            return Error(400, "invalid startDate format, use YYYY-MM-DD");
        if (!IsDate(endDate))
            return Error(400, "invalid endDate format, use YYYY-MM-DD");

        try
        {
            return Results.Json(await archive.RestoreRangeAsync(startDate, endDate, ct));
        }
        catch (Exception e) when (e is not OperationCanceledException)
        {
            return ServerError(log, $"RestoreArchiveHandler failed: {e.Message}");
        }
    }

    private static IResult TriggerPoll(PollingService polling, ILogger<Api> log)
    {
        log.LogInformation("Manual poll triggered");
        polling.TriggerOnce();
        return Results.Json(new { status = "polling started" });
    }

    // ── Helpers ──────────────────────────────────────────────────────────────

    /// <summary>Log category for the handlers.</summary>
    private sealed class Api;

    private static IResult Error(int status, string message) => Results.Json(new { error = message }, statusCode: status);

    /// <summary>Logs to the error file and hides details from the client, as the Go handlers do.</summary>
    private static IResult ServerError(ILogger log, string message)
    {
        log.LogError("{Message}", message);
        return Error(500, "internal server error");
    }

    private static string Query(HttpRequest req, string key) => req.Query[key].ToString();

    /// <summary>Fiber QueryInt: the default when missing or not an integer.</summary>
    internal static int QueryInt(HttpRequest req, string key, int def) =>
        int.TryParse(Query(req, key), NumberStyles.AllowLeadingSign, CultureInfo.InvariantCulture, out var n) ? n : def;

    /// <summary>Fiber QueryBool (strconv.ParseBool): 1/t/T/TRUE/true/True and 0/f/F/FALSE/false/False.</summary>
    internal static bool QueryBool(HttpRequest req, string key, bool def) => Query(req, key) switch
    {
        "1" or "t" or "T" or "TRUE" or "true" or "True" => true,
        "0" or "f" or "F" or "FALSE" or "false" or "False" => false,
        _ => def,
    };

    private static bool IsDate(string s) =>
        DateTime.TryParseExact(s, "yyyy-MM-dd", CultureInfo.InvariantCulture, DateTimeStyles.None, out _);

    /// <summary>
    /// Fiber BodyParser: JSON bodies only (any other content type is
    /// "Unprocessable Entity"); a parse failure returns the error text.
    /// </summary>
    private static async Task<(T? Value, string? Error)> ReadBodyAsync<T>(HttpRequest req, CancellationToken ct) where T : class
    {
        if (req.ContentType?.StartsWith("application/json", StringComparison.OrdinalIgnoreCase) != true)
            return (null, "Unprocessable Entity");
        try
        {
            var value = await JsonSerializer.DeserializeAsync<T>(req.Body, RequestJson, ct);
            return value == null ? (null, "invalid JSON body") : (value, null);
        }
        catch (JsonException e)
        {
            return (null, e.Message);
        }
    }
}
