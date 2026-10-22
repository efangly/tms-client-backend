using Microsoft.Extensions.Logging.Abstractions;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Infrastructure;
using TmsBackend.Models;
using TmsBackend.Protocol;
using TmsBackend.Services;
using TmsBackend.Tests.Transport;

namespace TmsBackend.Tests.Services;

// Ported from internal/services/polling_test.go, plus end-to-end cycles with fakes.
public class PollingServiceTests
{
    private readonly FakeMachineRepository _machines = new();
    private readonly FakeTempLogRepository _logs = new();
    private readonly FakeTempErrorRepository _errors = new();
    private readonly FakeProbeRequester _probes = new();
    private readonly FakeLegacyApi _legacy = new();
    private readonly FakeMqtt _mqtt = new();
    private readonly FakeDbPing _db = new();
    private readonly EventHub _events = new();
    private readonly ManualTimeProvider _time = ManualTimeProvider.AtThai(2026, 10, 22, 8, 1, 30);

    private PollingService NewService() => new(_machines, _logs, _errors, _probes, _legacy, _mqtt, _db, _events,
        new AppOptions(), new AppReadiness(), _time, NullLogger<PollingService>.Instance);

    private static MasterMachine Room(string ip = "192.168.1.10", int probeNo = 1, string chkReport = "0") => new()
    {
        MachineIP = ip,
        ProbeNo = probeNo,
        MachineName = "Test Room",
        MinTemp = 18.0,
        MaxTemp = 28.0,
        SType = "t",
        ChkReport = chkReport,
    };

    // ── Alert state machine ──────────────────────────────────────────────────

    [Fact]
    public async Task Normal_to_high_writes_temp_error_and_alerts()
    {
        var ps = NewService();
        ps.SetAlertState("192.168.1.10:1", "N");

        await ps.CheckProbeAlertAsync(Room(), 1, 30.0, default);
        await ps.DrainBackgroundAsync();

        Assert.Equal("H", ps.AlertState("192.168.1.10:1"));
        var e = Assert.Single(_errors.Inserted);
        Assert.Equal(("192.168.1.10", 1, 30.0, "p", "o", "t"), (e.MachineIP, e.ProbeNo, e.TempValue!.Value, e.TempStatus, e.ErrorType, e.SType));
        Assert.Equal(new DateTime(2026, 10, 22, 8, 1, 30), e.ErrorTime);

        var a = Assert.Single(_legacy.Alerts);
        Assert.Equal("high", a.AlertType);
        Assert.Equal("00000010", a.Status);
        Assert.Equal(3000, a.RealValue);
        Assert.Equal("20261022", a.Date);
        Assert.Equal("08:01:30", a.Time);
        Assert.Equal("Temperature สูงเกิน (ค่าปัจจุบัน: 30.00°C, ช่วง: 18.00-28.00°C) Test Room 2026/10/22 08:01:30", a.Message);
    }

    [Fact]
    public async Task Normal_to_low()
    {
        var ps = NewService();
        ps.SetAlertState("192.168.1.10:1", "N");

        await ps.CheckProbeAlertAsync(Room(), 1, 10.0, default);
        await ps.DrainBackgroundAsync();

        Assert.Equal("L", ps.AlertState("192.168.1.10:1"));
        Assert.Single(_errors.Inserted);
        Assert.Equal(("low", "00000011"), (_legacy.Alerts[0].AlertType, _legacy.Alerts[0].Status));
        Assert.StartsWith("Temperature ต่ำเกิน", _legacy.Alerts[0].Message);
    }

    [Fact]
    public async Task Staying_high_does_nothing()
    {
        var ps = NewService();
        ps.SetAlertState("192.168.1.10:1", "H");

        await ps.CheckProbeAlertAsync(Room(), 1, 32.0, default);
        await ps.DrainBackgroundAsync();

        Assert.Equal("H", ps.AlertState("192.168.1.10:1"));
        Assert.Empty(_errors.Inserted);
        Assert.Empty(_legacy.Alerts);
    }

    [Fact]
    public async Task High_to_normal_sends_recovery_without_temp_error()
    {
        var ps = NewService();
        ps.SetAlertState("192.168.1.10:1", "H");

        await ps.CheckProbeAlertAsync(Room(), 1, 22.0, default);
        await ps.DrainBackgroundAsync();

        Assert.Equal("N", ps.AlertState("192.168.1.10:1"));
        Assert.Empty(_errors.Inserted);
        var a = Assert.Single(_legacy.Alerts);
        Assert.Equal(("normal", "00000001"), (a.AlertType, a.Status));
        Assert.Equal("Temperature กลับเข้าช่วงปกติแล้ว (ค่าปัจจุบัน: 22.00°C, ช่วง: 18.00-28.00°C) Test Room 2026/10/22 08:01:30", a.Message);
    }

    [Fact]
    public async Task First_normal_reading_only_records_state()
    {
        var ps = NewService();

        await ps.CheckProbeAlertAsync(Room(), 1, 22.0, default);
        await ps.DrainBackgroundAsync();

        Assert.Equal("N", ps.AlertState("192.168.1.10:1"));
        Assert.Empty(_errors.Inserted);
        Assert.Empty(_legacy.Alerts);
    }

    [Fact]
    public async Task Duplicate_temp_error_keeps_the_new_state()
    {
        var ps = NewService();
        ps.SetAlertState("192.168.1.10:1", "N");
        _errors.InsertError = FakeErrors.DuplicateKey();

        await ps.CheckProbeAlertAsync(Room(), 1, 35.0, default);

        Assert.Equal("H", ps.AlertState("192.168.1.10:1"));
    }

    [Fact]
    public async Task Chk_report_repeats_once_per_poll_interval_and_tracks_chk_mon()
    {
        var ps = NewService();
        var machine = Room(chkReport: "1");

        await ps.CheckProbeAlertAsync(machine, 1, 30.0, default); // N→H: alert, chkMon=1
        _time.Advance(TimeSpan.FromSeconds(5));
        await ps.CheckProbeAlertAsync(machine, 1, 30.5, default); // still H, within interval: throttled
        _time.Advance(TimeSpan.FromMinutes(5));
        await ps.CheckProbeAlertAsync(machine, 1, 31.0, default); // still H, interval passed: repeat
        await ps.CheckProbeAlertAsync(machine, 1, 22.0, default); // H→N: recovery, chkMon=0
        await ps.DrainBackgroundAsync();

        Assert.Equal(["high", "high", "normal"], _legacy.Alerts.Select(a => a.AlertType));
        Assert.Equal(2, _errors.Inserted.Count);
        Assert.Equal([("192.168.1.10", 1, "1"), ("192.168.1.10", 1, "1"), ("192.168.1.10", 1, "0")], _machines.ChkMonUpdates);
    }

    [Fact]
    public async Task Humidity_alerts_use_humidity_label_and_unit()
    {
        var ps = NewService();
        var m = Room();
        m.SType = "h";
        m.MaxTemp = 70;

        await ps.CheckProbeAlertAsync(m, 2, 80.25, default);
        await ps.DrainBackgroundAsync();

        Assert.Equal("Humidity สูงเกิน (ค่าปัจจุบัน: 80.25%, ช่วง: 18.00-70.00%) Test Room 2026/10/22 08:01:30", _legacy.Alerts[0].Message);
    }

    // ── Machine cache ────────────────────────────────────────────────────────

    [Fact]
    public async Task Machine_cache_hit_skips_db()
    {
        var ps = NewService();
        ps.SeedMachineCache([new MasterMachine { MachineIP = "10.0.0.1", ProbeNo = 1 }], _time.GetUtcNow());
        _machines.GetAllError = new InvalidOperationException("must not query");

        var got = await ps.GetMachinesAsync(default);

        Assert.Equal("10.0.0.1", Assert.Single(got).MachineIP);
    }

    [Fact]
    public async Task Machine_cache_miss_queries_db_then_caches_for_a_minute()
    {
        var ps = NewService();
        _machines.Machines.Add(Room());

        Assert.Single(await ps.GetMachinesAsync(default));
        _machines.Machines.Clear();
        Assert.Single(await ps.GetMachinesAsync(default)); // cached
        _time.Advance(TimeSpan.FromMinutes(1));
        Assert.Empty(await ps.GetMachinesAsync(default)); // expired → fresh query
    }

    [Fact]
    public async Task Db_error_serves_stale_cache()
    {
        var ps = NewService();
        ps.SeedMachineCache([new MasterMachine { MachineIP = "stale-ip" }], _time.GetUtcNow() - TimeSpan.FromMinutes(2));
        _machines.GetAllError = new InvalidOperationException("connection refused");

        Assert.Equal("stale-ip", Assert.Single(await ps.GetMachinesAsync(default)).MachineIP);
    }

    [Fact]
    public async Task Db_error_without_cache_throws()
    {
        var ps = NewService();
        _machines.GetAllError = new InvalidOperationException("connection refused");
        await Assert.ThrowsAsync<InvalidOperationException>(() => ps.GetMachinesAsync(default));
    }

    // ── Poll & save ──────────────────────────────────────────────────────────

    [Fact]
    public async Task Poll_and_save_inserts_adjusted_readings_and_notifies()
    {
        var m = Room();
        m.AdjTemp = -0.5;
        _machines.Machines.Add(m);
        _probes.Readings["192.168.1.10"] = [new ProbeData(1, "A", 25.63, 6563, "00")];
        var sub = _events.Subscribe();
        var ps = NewService();

        await ps.PollAndSaveAsync(default);
        await ps.DrainBackgroundAsync();

        var log = Assert.Single(_logs.Inserted);
        Assert.Equal(25.13, log.TempValue);
        Assert.Equal(("192.168.1.10", 1, "Test Room", 6563, "N", "20261022", "08"),
            (log.MachineIP, log.ProbeNo, log.McuID, log.RealValue!.Value, log.Status, log.SDate, log.STime));
        Assert.Equal(new DateTime(2026, 10, 22, 8, 1, 30), log.InsertTime);
        Assert.Equal(TimeSpan.FromSeconds(5), _probes.Requests[0].Timeout);

        var payload = Assert.Single(_legacy.TempLogs);
        Assert.Equal(new TempLogPayload("Test Room", "00000110", 25.13, 6563, "20261022", "08"), payload);
        Assert.True(sub.Reader.TryRead(out var e));
        Assert.Equal(new DataSavedEvent(1, 0), e);
    }

    [Fact]
    public async Task Poll_skips_broken_mismatched_and_out_of_range_readings()
    {
        var humidity = Room(probeNo: 2);
        humidity.SType = "h";
        _machines.Machines.AddRange([Room(), humidity]);
        _probes.Readings["192.168.1.10"] =
        [
            new ProbeData(1, "A", 615.35, 65535, "00"), // broken sensor sentinel
            new ProbeData(2, "A", 25.0, 6500, "00"),    // temp reading for a humidity probe
            new ProbeData(1, "A", 75.0, 11500, "00"),   // above 50 °C → invalid
        ];
        var ps = NewService();

        await ps.PollAndSaveAsync(default);

        Assert.Empty(_logs.Inserted);
    }

    [Fact]
    public async Task Unconfigured_probe_borrows_first_probe_config()
    {
        _machines.Machines.Add(Room());
        _probes.Readings["192.168.1.10"] = [new ProbeData(3, "A", 20.0, 6000, "00")];
        var ps = NewService();

        await ps.PollAndSaveAsync(default);

        var log = Assert.Single(_logs.Inserted);
        Assert.Equal((3, "Test Room"), (log.ProbeNo, log.McuID));
    }

    [Fact]
    public async Task Duplicate_insert_is_not_counted_as_error()
    {
        _machines.Machines.Add(Room());
        _probes.Readings["192.168.1.10"] = [new ProbeData(1, "A", 25.0, 6500, "00")];
        _logs.InsertError = FakeErrors.DuplicateKey();
        var sub = _events.Subscribe();

        await NewService().PollAndSaveAsync(default);

        Assert.True(sub.Reader.TryRead(out var e));
        Assert.Equal(new DataSavedEvent(0, 0), e);
    }

    [Fact]
    public async Task Poll_skips_when_db_is_down()
    {
        _db.Ok = false;
        _machines.Machines.Add(Room());

        await NewService().PollAndSaveAsync(default);

        Assert.Empty(_probes.Requests);
    }

    // ── Alert loop ───────────────────────────────────────────────────────────

    [Fact]
    public async Task Check_alerts_publishes_mqtt_and_sse_with_status()
    {
        _machines.Machines.Add(Room());
        _probes.Readings["192.168.1.10"] = [new ProbeData(1, "A", 29.0, 6900, "00")];
        var sub = _events.SubscribeTemperature();
        var ps = NewService();

        await ps.CheckAlertsAsync(default);
        await ps.DrainBackgroundAsync();

        var p = Assert.Single(Assert.Single(_mqtt.Published));
        Assert.Equal(new MqttTemperaturePayload("Test Room", 29.0, "H", "t", "2026-10-22 08:01:30", 18, 28, "192.168.1.10", 1), p);
        Assert.True(sub.Reader.TryRead(out var events));
        Assert.Equal("H", Assert.Single(events).Status);
        Assert.Equal(TimeSpan.FromSeconds(3), _probes.Requests[0].Timeout);
        Assert.Equal("H", ps.AlertState("192.168.1.10:1"));
    }

    [Fact]
    public async Task Check_alerts_skips_mqtt_when_disconnected_but_still_notifies_sse()
    {
        var mqtt = new FakeMqtt(connected: false);
        var ps = new PollingService(_machines, _logs, _errors, _probes, _legacy, mqtt, _db, _events,
            new AppOptions(), new AppReadiness(), _time, NullLogger<PollingService>.Instance);
        _machines.Machines.Add(Room());
        _probes.Readings["192.168.1.10"] = [new ProbeData(1, "A", 22.0, 6200, "00")];
        var sub = _events.SubscribeTemperature();

        await ps.CheckAlertsAsync(default);
        await ps.DrainBackgroundAsync();

        Assert.Empty(mqtt.Published);
        Assert.True(sub.Reader.TryRead(out _));
    }

    [Fact]
    public async Task Monitored_machines_trigger_alert_check_only_when_chk_mon_set()
    {
        _machines.Machines.Add(Room());
        var ps = NewService();

        await ps.CheckMonitoredMachinesAsync(default);
        Assert.Empty(_probes.Requests);

        ps.SeedMachineCache([], default);
        _machines.Machines[0].ChkMon = "1";
        await ps.CheckMonitoredMachinesAsync(default);
        Assert.Single(_probes.Requests);
    }

    // ── Schedule reports ─────────────────────────────────────────────────────

    [Fact]
    public async Task Schedule_report_sent_for_matching_5_minute_slot()
    {
        var m = Room();
        m.Color = "0800,1200";
        var other = Room(ip: "10.0.0.2");
        other.Color = "0900";
        _machines.Machines.AddRange([m, other]);
        _logs.Latest[("192.168.1.10", 1)] = new TempLog { TempValue = 21.5 };
        var ps = NewService(); // clock is 08:01:30 → slot 0800

        await ps.CheckScheduleReportsAsync(default);
        await ps.DrainBackgroundAsync();

        var a = Assert.Single(_legacy.Alerts);
        Assert.Equal(("report", "00000110", 2150), (a.AlertType, a.Status, a.RealValue));
        Assert.Equal("[รายงาน] Temperature Test Room(1) ค่าปัจจุบัน: 21.50°C ช่วง: 18.00-28.00°C 2026/10/22 08:01:30", a.Message);
    }

    [Fact]
    public async Task Schedule_report_skipped_without_data()
    {
        var m = Room();
        m.Color = "0800";
        _machines.Machines.Add(m);

        await NewService().CheckScheduleReportsAsync(default);

        Assert.Empty(_legacy.Alerts);
    }

    [Theory]
    [InlineData(30.0, "t", true)]
    [InlineData(-100.0, "t", true)]
    [InlineData(50.01, "t", false)]
    [InlineData(100.0, "h", true)]
    [InlineData(-0.1, "H", false)]
    [InlineData(45.0, "p", true)]
    public void Valid_sensor_ranges(double value, string sType, bool expected) =>
        Assert.Equal(expected, PollingService.IsValidSensorValue(value, sType));
}

public class EventHubTests
{
    [Fact]
    public void Subscriber_receives_events()
    {
        var hub = new EventHub();
        var ch = hub.Subscribe();
        hub.Publish(new DataSavedEvent(5, 1));
        Assert.True(ch.Reader.TryRead(out var e));
        Assert.Equal(new DataSavedEvent(5, 1), e);
    }

    [Fact]
    public void Full_buffer_drops_new_events_without_blocking()
    {
        var hub = new EventHub();
        var ch = hub.Subscribe();
        for (var i = 0; i < 15; i++)
            hub.Publish(new DataSavedEvent(i, 0));

        var received = new List<int>();
        while (ch.Reader.TryRead(out var e))
            received.Add(e.Saved);
        Assert.Equal(Enumerable.Range(0, 10), received);
    }

    [Fact]
    public void Unsubscribe_removes_and_completes_channel()
    {
        var hub = new EventHub();
        var ch = hub.Subscribe();
        var t = hub.SubscribeTemperature();

        hub.Unsubscribe(ch);
        hub.UnsubscribeTemperature(t);
        hub.Unsubscribe(ch); // unknown/already removed: no-op

        Assert.Equal(0, hub.SubscriberCount);
        Assert.True(ch.Reader.Completion.IsCompleted);
        Assert.True(t.Reader.Completion.IsCompleted);
    }

    [Fact]
    public void Temperature_subscribers_receive_batches()
    {
        var hub = new EventHub();
        var ch = hub.SubscribeTemperature();
        hub.Publish([new TemperatureUpdateEvent("Room A", 22.5, "N", "t", "", 0, 0, "", 1)]);
        Assert.True(ch.Reader.TryRead(out var batch));
        Assert.Equal("Room A", Assert.Single(batch).MachineName);
    }
}
