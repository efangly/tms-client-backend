using System.Text.Json;
using TmsBackend.Data;
using TmsBackend.Models;

namespace TmsBackend.Tests.Data;

public class SqlBuilderTests
{
    [Fact]
    public void Insert_omits_zero_values_of_defaulted_columns_like_gorm()
    {
        var (sql, p) = SqlInsert.Build("master_machine",
        [
            new("machine_ip", "10.0.0.1"),
            new("color", "", HasDefault: true),
            new("probe_all", 0, HasDefault: true),
            new("adj_temp", null, HasDefault: true),
            new("sType", "h", HasDefault: true),
            new("min_temp", null),
        ]);

        Assert.Equal("INSERT INTO `master_machine` (`machine_ip`, `sType`, `min_temp`) VALUES (@p0, @p1, @p2)", sql);
        Assert.Equal("h", p.Get<string>("p1"));
        Assert.Null(p.Get<object?>("p2"));
    }

    private static readonly List<MasterMachine> Machines =
    [
        new() { MachineIP = "10.0.0.1", ProbeNo = 1, MachineName = "Fridge A" },
        new() { MachineIP = "10.0.0.1", ProbeNo = 2, MachineName = "Fridge A" },
        new() { MachineIP = "COM4", ProbeNo = 1, MachineName = "Lab" },
    ];

    [Fact]
    public void Device_filter_resolves_ips_names_and_probe_pairs()
    {
        var f = DeviceFilter.Parse("Fridge A,COM4:1,unknown,10.0.0.1:x", Machines);

        Assert.Equal(["10.0.0.1"], f.Ips);
        Assert.Equal([("COM4", 1)], f.Pairs);

        var p = new Dapper.DynamicParameters();
        Assert.Equal("(machine_ip IN @devIps OR (machine_ip = @devIp0 AND probe_no = @devProbe0))", f.ToSql(p));
    }

    [Fact]
    public void Device_filter_with_nothing_resolved_matches_nothing()
    {
        Assert.Equal("1 = 0", DeviceFilter.Parse("nope", Machines).ToSql(new Dapper.DynamicParameters()));
    }

    [Fact]
    public void Temp_log_query_applies_dates_only_when_both_set()
    {
        var (sql, _) = TempLogRepository.BuildQuery(new TempLogQuery { StartDate = "2026-10-01", Descending = true, Limit = 100 }, "temp_log");
        Assert.Equal("SELECT * FROM temp_log ORDER BY insert_time DESC LIMIT @limit", sql);

        var (sql2, p2) = TempLogRepository.BuildQuery(new TempLogQuery
        {
            StartDate = "2026-10-01",
            EndDate = "2026-10-02",
            Devices = DeviceFilter.Parse("Lab", Machines),
            Limit = -1,
        }, "temp_log_archive");
        Assert.Equal("SELECT * FROM temp_log_archive WHERE insert_time BETWEEN @start AND @end AND (machine_ip IN @devIps) ORDER BY insert_time ASC", sql2);
        Assert.Equal("2026-10-01 00:00:00", p2.Get<string>("start"));
        Assert.Equal("2026-10-02 23:59:59", p2.Get<string>("end"));
    }

    [Fact]
    public void Float_temp_values_normalize_to_shortest_decimal()
    {
        Assert.Equal(25.4, TempLogRow.ToDouble(25.4f));
        Assert.Equal(-48.24, TempLogRow.ToDouble(-48.24f));
        Assert.Equal(25.4, TempLogRow.ToDouble(25.4d));
        Assert.Null(TempLogRow.ToDouble(null));
        Assert.Null(TempLogRow.ToDouble(DBNull.Value));
    }

    [Fact]
    public void Machine_update_maps_json_and_go_names_and_drops_primary_keys()
    {
        var body = JsonSerializer.Deserialize<Dictionary<string, JsonElement>>(
            """{"machineIp":"x","probeNo":3,"machineName":"New","minTemp":2.5,"ChkReport":"1","CHKMON":"0","sType":null,"color":"0800"}""")!;

        var cols = MachineUpdate.ToColumns(body);

        Assert.Equal(new Dictionary<string, object?>
        {
            ["machine_name"] = "New",
            ["min_temp"] = 2.5,
            ["chkReport"] = "1",
            ["chkMon"] = "0",
            ["sType"] = null,
            ["color"] = "0800",
        }, cols);
    }

    [Fact]
    public void Machine_update_rejects_unknown_columns()
    {
        var body = JsonSerializer.Deserialize<Dictionary<string, JsonElement>>("""{"port":1}""")!;
        Assert.Throws<UnknownColumnException>(() => MachineUpdate.ToColumns(body));

        var injection = JsonSerializer.Deserialize<Dictionary<string, JsonElement>>("""{"color` = 1; --":1}""")!;
        Assert.Throws<UnknownColumnException>(() => MachineUpdate.ToColumns(injection));
    }

    [Fact]
    public void Archive_table_names_are_validated()
    {
        ArchiveRepository.EnsureValidTable("temp_log_backup");
        Assert.Throws<ArgumentException>(() => ArchiveRepository.EnsureValidTable("temp_log; DROP TABLE x"));
    }

    [Fact]
    public void Archive_insert_ignores_duplicates()
    {
        var (sql, _) = ArchiveRepository.BuildArchiveInsert([new TempLog { MachineIP = "a" }, new TempLog { MachineIP = "b" }]);
        Assert.Contains("VALUES (@ip0,", sql);
        Assert.Contains("(@ip1,", sql);
        Assert.EndsWith("ON DUPLICATE KEY UPDATE machine_ip = machine_ip", sql);
    }
}
