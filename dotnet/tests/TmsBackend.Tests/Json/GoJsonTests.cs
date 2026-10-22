using System.Text.Json;
using TmsBackend.Json;
using TmsBackend.Models;

namespace TmsBackend.Tests.Json;

public class GoJsonTests
{
    private static string Offset(DateTime t)
    {
        var o = TimeZoneInfo.Local.GetUtcOffset(t);
        return o == TimeSpan.Zero ? "Z" : (o < TimeSpan.Zero ? "-" : "+") + o.Duration().ToString(@"hh\:mm");
    }

    [Fact]
    public void Time_matches_go_rfc3339_with_local_offset()
    {
        var t = new DateTime(2026, 10, 22, 15, 33, 53);
        Assert.Equal("2026-10-22T15:33:53" + Offset(t), GoTimeConverter.Format(t));
    }

    [Fact]
    public void Fractional_seconds_are_trimmed_like_go()
    {
        var t = new DateTime(2026, 10, 22, 15, 33, 53).AddMilliseconds(120);
        Assert.Equal("2026-10-22T15:33:53.12" + Offset(t), GoTimeConverter.Format(t));
    }

    [Fact]
    public void Zero_time_is_go_zero_value()
    {
        Assert.Equal("0001-01-01T00:00:00Z", GoTimeConverter.Format(DateTime.MinValue));
        Assert.Equal(DateTime.MinValue, GoTimeConverter.Parse("0001-01-01T00:00:00Z"));
    }

    [Fact]
    public void Parse_returns_local_wall_clock()
    {
        var local = new DateTime(2026, 10, 22, 15, 33, 53);
        var parsed = GoTimeConverter.Parse(GoTimeConverter.Format(local));
        Assert.Equal(local, parsed);
        Assert.Equal(DateTimeKind.Unspecified, parsed.Kind);
    }

    [Fact]
    public void TempLog_serializes_with_go_field_names_and_raw_thai()
    {
        var t = new DateTime(2026, 10, 22, 15, 33, 53);
        var log = new TempLog
        {
            MachineIP = "COM4",
            ProbeNo = 1,
            McuID = "ตู้เย็น 1",
            TempValue = 25.4,
            RealValue = 6540,
            Status = "N",
            SendTime = null,
            InsertTime = t,
            SDate = "20261022",
            STime = "15",
        };

        var json = JsonSerializer.Serialize(log, GoJson.Options);

        Assert.Equal(
            "{\"machineIp\":\"COM4\",\"probeNo\":1,\"mcuId\":\"ตู้เย็น 1\",\"tempValue\":25.4,\"realValue\":6540,\"status\":\"N\"," +
            $"\"sendTime\":null,\"insertTime\":\"2026-10-22T15:33:53{Offset(t)}\",\"sDate\":\"20261022\",\"sTime\":\"15\"}}",
            json);
    }

    [Fact]
    public void Reads_go_archive_file_rows()
    {
        const string goJson = """[{"machineIp":"192.168.1.111","probeNo":1,"mcuId":"A","tempValue":4.5,"realValue":4450,"status":"N","sendTime":"2026-09-01T10:00:00+07:00","insertTime":"2026-09-01T10:00:01+07:00","sDate":"20260901","sTime":"10"}]""";

        var rows = JsonSerializer.Deserialize<List<TempLog>>(goJson, GoJson.Options)!;

        var r = Assert.Single(rows);
        Assert.Equal("192.168.1.111", r.MachineIP);
        Assert.Equal(4.5, r.TempValue);
        var expected = TimeZoneInfo.ConvertTime(new DateTimeOffset(2026, 9, 1, 10, 0, 1, TimeSpan.FromHours(7)), TimeZoneInfo.Local).DateTime;
        Assert.Equal(expected, r.InsertTime);
    }

    [Fact]
    public void Password_is_never_serialized()
    {
        var json = JsonSerializer.Serialize(new MasterUser { Username = "admin", Password = "secret" }, GoJson.Options);
        Assert.DoesNotContain("secret", json);
    }
}
