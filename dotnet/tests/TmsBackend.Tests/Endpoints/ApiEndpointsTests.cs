using System.Net;
using System.Text;
using System.Text.Json;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.TestHost;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Endpoints;
using TmsBackend.Infrastructure;
using TmsBackend.Json;
using TmsBackend.Models;
using TmsBackend.Services;
using TmsBackend.Tests.Services;
using TmsBackend.Tests.Transport;

namespace TmsBackend.Tests.Endpoints;

/// <summary>The real route table on an in-memory server, backed by fakes (Go: handlers_test.go).</summary>
internal sealed class ApiFixture : IAsyncDisposable
{
    public FakeMachineRepository Machines { get; } = new();
    public FakeTempLogRepository Logs { get; } = new();
    public FakeTempErrorRepository Errors { get; } = new();
    public FakeArchiveRepository Archive { get; } = new();
    public FakeProbeRequester Probes { get; } = new();
    public FakeDbPing Db { get; } = new();
    public FakeMqtt Mqtt { get; } = new(connected: false);
    public EventHub Events { get; } = new();
    public ManualTimeProvider Time { get; } = ManualTimeProvider.AtThai(2026, 10, 22, 8, 0);
    public string ArchiveDir { get; } = Directory.CreateTempSubdirectory().FullName;

    private WebApplication? _app;
    private HttpClient? _client;

    public HttpClient Client => _client ?? throw new InvalidOperationException("call StartAsync first");

    public async Task StartAsync()
    {
        var builder = WebApplication.CreateBuilder();
        builder.WebHost.UseTestServer();
        builder.Logging.ClearProviders();
        var options = new AppOptions { ArchiveDir = ArchiveDir };
        var s = builder.Services;
        s.AddSingleton(options);
        s.AddSingleton<IMachineRepository>(Machines);
        s.AddSingleton<ITempLogRepository>(Logs);
        s.AddSingleton<ITempErrorRepository>(Errors);
        s.AddSingleton<IArchiveRepository>(Archive);
        s.AddSingleton<IDbPing>(Db);
        s.AddSingleton<IMqttPublisher>(Mqtt);
        s.AddSingleton(Events);
        s.AddSingleton<TimeProvider>(Time);
        s.AddSingleton(new ArchiveService(Archive, options, new AppReadiness(), Time, NullLogger<ArchiveService>.Instance));
        s.AddSingleton(new PollingService(Machines, Logs, Errors, Probes, new FakeLegacyApi(false), Mqtt, Db, Events,
            options, new AppReadiness(), Time, NullLogger<PollingService>.Instance));
        s.ConfigureHttpJsonOptions(o => GoJson.Configure(o.SerializerOptions));

        _app = builder.Build();
        _app.MapTmsApi();
        await _app.StartAsync();
        _client = _app.GetTestClient();
    }

    public async ValueTask DisposeAsync()
    {
        if (_app != null)
            await _app.DisposeAsync();
        Directory.Delete(ArchiveDir, true);
    }
}

public class ApiEndpointsTests : IAsyncLifetime
{
    private readonly ApiFixture _f = new();

    public Task InitializeAsync() => _f.StartAsync();

    public async Task DisposeAsync() => await _f.DisposeAsync();

    private static MasterMachine Room(string ip = "192.168.1.10", int probeNo = 1) => new()
    {
        MachineIP = ip,
        ProbeNo = probeNo,
        ProbeAll = 4,
        MachineName = "Room A",
        Color = "FF5733",
        ChkOnline = "1",
        MinTemp = 18,
        MaxTemp = 28,
        AdjTemp = 0,
        SType = "t",
    };

    private async Task<(HttpStatusCode Status, JsonElement Body)> Send(HttpMethod method, string url, string? json = null, string contentType = "application/json")
    {
        using var req = new HttpRequestMessage(method, url);
        if (json != null)
            req.Content = new StringContent(json, Encoding.UTF8, contentType);
        using var res = await _f.Client.SendAsync(req);
        var text = await res.Content.ReadAsStringAsync();
        return (res.StatusCode, text == "" ? default : JsonDocument.Parse(text).RootElement.Clone());
    }

    private Task<(HttpStatusCode Status, JsonElement Body)> Get(string url) => Send(HttpMethod.Get, url);

    // ── Health ───────────────────────────────────────────────────────────────

    [Fact]
    public async Task Health_ok_when_db_up()
    {
        var (status, body) = await Get("/health");
        Assert.Equal(HttpStatusCode.OK, status);
        Assert.Equal("ok", body.GetProperty("status").GetString());
        Assert.True(body.GetProperty("db").GetBoolean());
        Assert.False(body.GetProperty("mqtt").GetBoolean());
        Assert.Equal(AppVersion.Value, body.GetProperty("version").GetString());
    }

    [Fact]
    public async Task Health_degraded_503_when_db_down()
    {
        _f.Db.Ok = false;
        var (status, body) = await Get("/health");
        Assert.Equal(HttpStatusCode.ServiceUnavailable, status);
        Assert.Equal("degraded", body.GetProperty("status").GetString());
    }

    // ── Devices ──────────────────────────────────────────────────────────────

    [Fact]
    public async Task Get_devices_returns_go_shaped_rows()
    {
        _f.Machines.Machines.Add(Room());
        var (status, body) = await Get("/api/devices");

        Assert.Equal(HttpStatusCode.OK, status);
        var d = Assert.Single(body.EnumerateArray().ToList());
        Assert.Equal(
            ["machineIp", "probeNo", "probeAll", "machineName", "color", "chkOnline", "chkSms", "chkMail", "chkMon", "chkLine", "chkReport", "minTemp", "maxTemp", "adjTemp", "sType", "port"],
            d.EnumerateObject().Select(p => p.Name));
        Assert.Equal("192.168.1.10", d.GetProperty("machineIp").GetString());
    }

    [Fact]
    public async Task Get_devices_db_error_is_500()
    {
        _f.Machines.GetAllError = new InvalidOperationException("boom");
        var (status, body) = await Get("/api/devices");
        Assert.Equal(HttpStatusCode.InternalServerError, status);
        Assert.Equal("internal server error", body.GetProperty("error").GetString());
    }

    [Fact]
    public async Task Get_device_by_ip_and_probe()
    {
        _f.Machines.Machines.AddRange([Room(), Room(probeNo: 2)]);
        Assert.Equal(1, (await Get("/api/devices/192.168.1.10")).Body.GetProperty("probeNo").GetInt32());
        Assert.Equal(2, (await Get("/api/devices/192.168.1.10?probeNo=2")).Body.GetProperty("probeNo").GetInt32());

        var (status, body) = await Get("/api/devices/10.9.9.9");
        Assert.Equal(HttpStatusCode.NotFound, status);
        Assert.Equal("Machine not found", body.GetProperty("error").GetString());
    }

    [Fact]
    public async Task Create_device_defaults_probe_and_stype()
    {
        var (status, body) = await Send(HttpMethod.Post, "/api/devices", """{"machineIp":"10.0.0.5","MACHINENAME":"Fridge"}""");

        Assert.Equal(HttpStatusCode.Created, status);
        Assert.Equal(1, body.GetProperty("probeNo").GetInt32());
        Assert.Equal("t", body.GetProperty("sType").GetString());
        var created = Assert.Single(_f.Machines.Machines);
        Assert.Equal("Fridge", created.MachineName); // keys match case-insensitively, as encoding/json
    }

    [Fact]
    public async Task Create_device_bad_json_or_content_type_is_400()
    {
        Assert.Equal(HttpStatusCode.BadRequest, (await Send(HttpMethod.Post, "/api/devices", "{bad")).Status);
        var (status, body) = await Send(HttpMethod.Post, "/api/devices", """{"machineIp":"x"}""", "text/plain");
        Assert.Equal(HttpStatusCode.BadRequest, status);
        Assert.Equal("Unprocessable Entity", body.GetProperty("error").GetString());
    }

    [Fact]
    public async Task Create_device_db_error_is_500()
    {
        _f.Machines.WriteError = new InvalidOperationException("dup");
        Assert.Equal(HttpStatusCode.InternalServerError, (await Send(HttpMethod.Post, "/api/devices", """{"machineIp":"x"}""")).Status);
    }

    [Fact]
    public async Task Update_device_translates_keys_and_strips_primary_keys()
    {
        _f.Machines.Machines.AddRange([Room(), Room(probeNo: 2)]);

        var (status, body) = await Send(HttpMethod.Put, "/api/devices/192.168.1.10?probeNo=2",
            """{"machineIp":"evil","probeNo":9,"machineName":"Renamed","minTemp":2.5}""");

        Assert.Equal(HttpStatusCode.OK, status);
        Assert.Equal("Renamed", body.GetProperty("machineName").GetString());
        Assert.Equal(2, body.GetProperty("probeNo").GetInt32());
        var (ip, probe, cols) = Assert.Single(_f.Machines.Updates);
        Assert.Equal(("192.168.1.10", 2), (ip, probe));
        Assert.Equal(["machine_name", "min_temp"], cols.Keys.Order());
    }

    [Fact]
    public async Task Update_device_unknown_field_is_500_and_missing_is_404()
    {
        _f.Machines.Machines.Add(Room());
        Assert.Equal(HttpStatusCode.InternalServerError, (await Send(HttpMethod.Put, "/api/devices/192.168.1.10", """{"nope":1}""")).Status);
        Assert.Equal(HttpStatusCode.NotFound, (await Send(HttpMethod.Put, "/api/devices/1.1.1.1", "{}")).Status);
    }

    [Fact]
    public async Task Delete_device_by_probe_or_all()
    {
        var (status, body) = await Send(HttpMethod.Delete, "/api/devices/192.168.1.10?probeNo=2");
        Assert.Equal(HttpStatusCode.OK, status);
        Assert.True(body.GetProperty("success").GetBoolean());
        await Send(HttpMethod.Delete, "/api/devices/192.168.1.10");
        Assert.Equal([("192.168.1.10", (int?)2), ("192.168.1.10", null)], _f.Machines.Deletes);
    }

    // ── Machines ─────────────────────────────────────────────────────────────

    [Fact]
    public async Task Get_machines_with_status()
    {
        _f.Machines.Machines.AddRange([Room(), Room(ip: "10.0.0.2")]);
        var local = _f.Time.GetLocalNow().DateTime;
        _f.Machines.LatestReadings.Add(new LatestReading("192.168.1.10", 1, 24.5, local.AddMinutes(-3)));
        _f.Machines.LatestReadings.Add(new LatestReading("10.0.0.2", 1, 20.0, local.AddMinutes(-11)));

        var (_, body) = await Get("/api/machines");
        var rows = body.EnumerateArray().ToList();

        Assert.Equal("Online", rows[0].GetProperty("onlineStatus").GetString());
        Assert.Equal(24.5, rows[0].GetProperty("currentValue").GetDouble());
        Assert.Equal(local.AddMinutes(-3).ToString("yyyy-MM-dd HH:mm:ss"), rows[0].GetProperty("lastUpdate").GetString());
        Assert.Equal("Room A", rows[0].GetProperty("machineName").GetString()); // flattened MasterMachine fields
        Assert.Equal("Offline", rows[1].GetProperty("onlineStatus").GetString());
    }

    [Fact]
    public async Task Get_machines_without_readings_are_offline_with_nulls()
    {
        _f.Machines.Machines.Add(Room());
        var row = (await Get("/api/machines")).Body[0];
        Assert.Equal("Offline", row.GetProperty("onlineStatus").GetString());
        Assert.Equal(JsonValueKind.Null, row.GetProperty("currentValue").ValueKind);
        Assert.Equal(JsonValueKind.Null, row.GetProperty("lastUpdate").ValueKind);
    }

    [Fact]
    public async Task Update_machine_by_ip_and_probe()
    {
        _f.Machines.Machines.Add(Room());
        var (status, body) = await Send(HttpMethod.Put, "/api/machines/192.168.1.10/1", """{"chkReport":"1"}""");
        Assert.Equal(HttpStatusCode.OK, status);
        Assert.Equal("1", body.GetProperty("chkReport").GetString());
        Assert.Equal(HttpStatusCode.NotFound, (await Send(HttpMethod.Put, "/api/machines/192.168.1.10/7", "{}")).Status);
    }

    // ── Schedule ─────────────────────────────────────────────────────────────

    [Fact]
    public async Task Schedule_get_set_add_remove()
    {
        var m = Room();
        m.Color = "";
        _f.Machines.Machines.Add(m);

        Assert.Equal(JsonValueKind.Null, (await Get("/api/machines/192.168.1.10/1/schedule")).Body.GetProperty("times").ValueKind);

        var (status, body) = await Send(HttpMethod.Put, "/api/machines/192.168.1.10/1/schedule", """{"times":["0800"," 1200","0800"]}""");
        Assert.Equal(HttpStatusCode.OK, status);
        Assert.Equal(["0800", "1200"], body.GetProperty("times").EnumerateArray().Select(t => t.GetString()));
        Assert.Equal("0800,1200", m.Color);

        body = (await Send(HttpMethod.Post, "/api/machines/192.168.1.10/1/schedule/1700")).Body;
        Assert.Equal(["0800", "1200", "1700"], body.GetProperty("times").EnumerateArray().Select(t => t.GetString()));

        body = (await Send(HttpMethod.Delete, "/api/machines/192.168.1.10/1/schedule/1200")).Body;
        Assert.Equal(["0800", "1700"], body.GetProperty("times").EnumerateArray().Select(t => t.GetString()));

        body = (await Send(HttpMethod.Put, "/api/machines/192.168.1.10/1/schedule", """{"times":[]}""")).Body;
        Assert.Equal(JsonValueKind.Null, body.GetProperty("times").ValueKind);
    }

    [Fact]
    public async Task Schedule_validation()
    {
        _f.Machines.Machines.Add(Room());

        var (status, body) = await Send(HttpMethod.Put, "/api/machines/192.168.1.10/1/schedule", """{"times":["0801"]}""");
        Assert.Equal(HttpStatusCode.BadRequest, status);
        Assert.Equal("invalid time format: 0801 (expected HHmm, e.g. 0800)", body.GetProperty("error").GetString());

        (status, body) = await Send(HttpMethod.Put, "/api/machines/192.168.1.10/1/schedule", """{"times":["0000","0100","0200","0300","0400","0500","0600"]}""");
        Assert.Equal("maximum 6 schedule times allowed", body.GetProperty("error").GetString());

        Assert.Equal(HttpStatusCode.BadRequest, (await Send(HttpMethod.Post, "/api/machines/192.168.1.10/1/schedule/2400")).Status);
        Assert.Equal(HttpStatusCode.NotFound, (await Get("/api/machines/192.168.1.10/x/schedule")).Status);
    }

    [Theory]
    [InlineData("0800", true)]
    [InlineData("2355", true)]
    [InlineData("0801", false)]
    [InlineData("2400", false)]
    [InlineData("800", false)]
    [InlineData("ab00", false)]
    public void Hhmm_validation(string s, bool ok) => Assert.Equal(ok, ApiEndpoints.IsValidHHmm(s));

    // ── Temperature logs / errors ────────────────────────────────────────────

    private static TempLog Log(string ip, int probe, double? value, DateTime at) => new()
    {
        MachineIP = ip, ProbeNo = probe, TempValue = value, InsertTime = at, McuID = "A", Status = "N",
    };

    [Fact]
    public async Task Temp_logs_default_limit_and_filters()
    {
        _f.Machines.Machines.Add(Room());
        await Get("/api/temp-logs");
        await Get("/api/temp-logs?startDate=2026-10-01&endDate=2026-10-02&limit=5&devices=Room%20A:1");

        var (q1, _) = _f.Logs.Queries[0];
        Assert.Equal((100, true, (DeviceFilter?)null), (q1.Limit!.Value, q1.Descending, q1.Devices));
        var (q2, _) = _f.Logs.Queries[1];
        Assert.Equal(("2026-10-01", "2026-10-02", 5), (q2.StartDate, q2.EndDate, q2.Limit!.Value));
        Assert.Equal([("192.168.1.10", 1)], q2.Devices!.Pairs);
    }

    [Fact]
    public async Task Temp_logs_include_archive_merges_newest_first_with_limit()
    {
        var day = new DateTime(2026, 9, 1, 10, 0, 0);
        _f.Logs.Live.Add(Log("a", 1, 1, day.AddHours(2)));
        _f.Logs.Archive.AddRange([Log("a", 1, 2, day.AddHours(3)), Log("a", 1, 3, day)]);

        var (_, body) = await Get("/api/temp-logs?startDate=2026-09-01&endDate=2026-09-01&includeArchive=true&limit=2");

        Assert.Equal([2.0, 1.0], body.EnumerateArray().Select(r => r.GetProperty("tempValue").GetDouble()));
        Assert.Contains(_f.Logs.Queries, q => q.FromArchive);
    }

    [Fact]
    public async Task Report_validates_dates()
    {
        Assert.Equal("startDate and endDate are required", (await Get("/api/reports/templog?startDate=2026-10-01")).Body.GetProperty("error").GetString());
        Assert.Equal("invalid startDate format, use YYYY-MM-DD", (await Get("/api/reports/templog?startDate=2026-1-01&endDate=2026-10-02")).Body.GetProperty("error").GetString());
        Assert.Equal("invalid endDate format, use YYYY-MM-DD", (await Get("/api/reports/templog?startDate=2026-10-01&endDate=2026-02-30")).Body.GetProperty("error").GetString());
    }

    [Fact]
    public async Task Report_builds_series_per_probe_with_name_fallback()
    {
        _f.Machines.Machines.Add(Room());
        var t = new DateTime(2026, 10, 20, 8, 0, 0);
        _f.Logs.Live.AddRange([Log("192.168.1.10", 1, 24.5, t), Log("192.168.1.10", 1, null, t.AddMinutes(5)), Log("10.0.0.9", 2, 5.0, t)]);

        var (status, body) = await Get("/api/reports/templog?startDate=2026-10-20&endDate=2026-10-21");

        Assert.Equal(HttpStatusCode.OK, status);
        Assert.Equal(3, body.GetProperty("data").GetArrayLength());
        var series = body.GetProperty("series").EnumerateArray().ToList();
        Assert.Equal(["Room A-P1", "10.0.0.9-P2"], series.Select(s => s.GetProperty("label").GetString()));
        var point = Assert.Single(series[0].GetProperty("data").EnumerateArray().ToList());
        Assert.Equal(GoTimeConverter.Format(t), point.GetProperty("x").GetString());
        Assert.Equal(24.5, point.GetProperty("y").GetDouble());
        Assert.DoesNotContain(_f.Logs.Queries, q => q.FromArchive); // inside retention
    }

    [Fact]
    public async Task Report_empty_series_is_null_and_old_range_reads_archive()
    {
        var (_, body) = await Get("/api/reports/templog?startDate=2026-08-01&endDate=2026-08-31");
        Assert.Equal(JsonValueKind.Null, body.GetProperty("series").ValueKind);
        Assert.Contains(_f.Logs.Queries, q => q.FromArchive);
    }

    [Fact]
    public async Task Temp_errors_pass_filters_and_limit()
    {
        await Get("/api/temp-errors?startDate=2026-10-01&endDate=2026-10-02&limit=7");
        await Get("/api/temp-errors?limit=abc");
        Assert.Equal([("2026-10-01", "2026-10-02", 7), ("", "", 100)], _f.Errors.Queries);
    }

    // ── Archive / poll ───────────────────────────────────────────────────────

    [Fact]
    public async Task Archive_run_list_restore()
    {
        var (status, body) = await Send(HttpMethod.Post, "/api/archive/run");
        Assert.Equal(HttpStatusCode.OK, status);
        Assert.Equal(0, body.GetProperty("daysArchived").GetInt32());

        _f.Archive.Manifests.Add(new ArchiveManifest { ID = 1, SourceTable = "temp_log", PeriodDate = "2026-08-01", FilePath = "x.json", RowCount = 3 });
        var list = (await Get("/api/archive")).Body;
        Assert.Equal("2026-08-01", list[0].GetProperty("periodDate").GetString());

        Assert.Equal(HttpStatusCode.BadRequest, (await Send(HttpMethod.Post, "/api/archive/restore?startDate=2026-08-01")).Status);
        (status, body) = await Send(HttpMethod.Post, "/api/archive/restore?startDate=2026-08-01&endDate=2026-08-31");
        Assert.Equal(HttpStatusCode.OK, status);
        Assert.Equal(0, body.GetProperty("daysRestored").GetInt32()); // file missing → skipped
    }

    [Fact]
    public async Task Trigger_poll_starts_a_cycle()
    {
        _f.Machines.Machines.Add(Room());
        var (status, body) = await Send(HttpMethod.Post, "/api/poll");

        Assert.Equal(HttpStatusCode.OK, status);
        Assert.Equal("polling started", body.GetProperty("status").GetString());
        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (_f.Probes.Requests.Count == 0 && DateTime.UtcNow < deadline)
            await Task.Delay(10);
        Assert.NotEmpty(_f.Probes.Requests);
    }
}

public class TemperatureStreamTests : IAsyncLifetime
{
    private readonly ApiFixture _f = new();

    public Task InitializeAsync() => _f.StartAsync();

    public async Task DisposeAsync() => await _f.DisposeAsync();

    [Fact]
    public async Task Streams_connected_refresh_and_temperature_events()
    {
        using var res = await _f.Client.GetAsync("/api/temperature-stream", HttpCompletionOption.ResponseHeadersRead);
        Assert.Equal("text/event-stream", res.Content.Headers.ContentType?.MediaType ?? res.Headers.GetValues("Content-Type").First());
        using var reader = new StreamReader(await res.Content.ReadAsStreamAsync());

        async Task<string> NextEvent()
        {
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(5));
            string? line;
            while ((line = await reader.ReadLineAsync(cts.Token)) == "")
            {
            }
            return line!;
        }

        Assert.Equal($"data: {{\"type\":\"connected\",\"version\":\"{AppVersion.Value}\"}}", await NextEvent());

        // Wait until the handler has subscribed before publishing.
        var deadline = DateTime.UtcNow.AddSeconds(5);
        while (_f.Events.SubscriberCount < 2 && DateTime.UtcNow < deadline)
            await Task.Delay(10);

        _f.Events.Publish(new DataSavedEvent(3, 1));
        Assert.Equal("data: {\"type\":\"refresh\",\"saved\":3,\"errors\":1}", await NextEvent());

        _f.Events.Publish([new TemperatureUpdateEvent("Room A", 22.5, "N", "t", "2026-10-22 08:00:00", 18, 28, "192.168.1.10", 1)]);
        var temp = await NextEvent();
        Assert.StartsWith("data: {\"count\":1,\"data\":[{\"machineName\":\"Room A\",\"tempValue\":22.5,\"status\":\"N\",\"type\":\"t\"", temp);
        Assert.EndsWith("\"type\":\"temperature\"}", temp);
    }

    [Fact]
    public async Task Client_disconnect_unsubscribes()
    {
        using (var res = await _f.Client.GetAsync("/api/temperature-stream", HttpCompletionOption.ResponseHeadersRead))
        {
            var deadline = DateTime.UtcNow.AddSeconds(5);
            while (_f.Events.SubscriberCount < 2 && DateTime.UtcNow < deadline)
                await Task.Delay(10);
            Assert.Equal(2, _f.Events.SubscriberCount);
        }

        var until = DateTime.UtcNow.AddSeconds(5);
        while (_f.Events.SubscriberCount > 0 && DateTime.UtcNow < until)
            await Task.Delay(10);
        Assert.Equal(0, _f.Events.SubscriberCount);
    }
}
