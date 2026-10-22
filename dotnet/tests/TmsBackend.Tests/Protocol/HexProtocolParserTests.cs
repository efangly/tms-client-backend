using TmsBackend.Protocol;

namespace TmsBackend.Tests.Protocol;

// Ported from internal/tcpclient/tcpclient_test.go — same frames, same expectations.
public class HexProtocolParserTests
{
    private static List<ProbeData> Parse(params byte[] data) => HexProtocolParser.Parse(data, "192.168.1.1");

    [Fact]
    public void One_probe_nine_bytes()
    {
        // raw 0x19A3 = 6563 → (6563-4000)*0.01 = 25.63 °C
        var p = Assert.Single(Parse(0x41, 0x41, 0x5a, 0x00, 0x5a, 0x19, 0xa3, 0x5a, 0x0d));
        Assert.Equal(new ProbeData(1, "A", 25.63, 6563, "00"), p);
    }

    [Fact]
    public void Ultra_low_header_4142()
    {
        // raw 0x02D4 = 724 → -(724*0.01) - 48.24 = -55.48 °C
        var p = Assert.Single(Parse(0x41, 0x42, 0x5a, 0x00, 0x5a, 0x02, 0xd4, 0x5a, 0x0d));
        Assert.Equal(724, p.RealValue);
        Assert.Equal(-55.48, p.TempValue);
    }

    [Fact]
    public void Real_device_frame_from_com4()
    {
        // Captured from the probe on COM4 during the Phase 0 spike.
        var p = Assert.Single(HexProtocolParser.Parse(Convert.FromHexString("41415A035A198C5A0D"), "COM4"));
        Assert.Equal(25.4, p.TempValue);
    }

    [Theory]
    [InlineData(new byte[] { 0x41, 0x43, 0x5a, 0x00, 0x5a, 0x02, 0xd4, 0x5a, 0x0d })] // unknown header
    [InlineData(new byte[] { 0xFF, 0xFE, 0x5a, 0x00, 0x5a, 0x19, 0xa3, 0x5a, 0x0d })] // invalid header
    [InlineData(new byte[] { 0x41, 0x41, 0x5a, 0x00, 0x5a, 0x19 })]                   // too short
    [InlineData(new byte[] { 0x41, 0x41, 0x5a, 0x00, 0xBB, 0x19, 0xa3, 0x5a, 0x0d })] // bad separator at [4]
    public void Rejected_frames_yield_no_probes(byte[] data)
    {
        Assert.Empty(HexProtocolParser.Parse(data, "192.168.1.1"));
    }

    [Fact]
    public void Two_probes_twelve_bytes()
    {
        var probes = Parse(0x41, 0x41, 0x5a, 0x03, 0x5a, 0x19, 0xa3, 0x5a, 0x19, 0xae, 0x5a, 0x0d);
        Assert.Equal(2, probes.Count);
        Assert.Equal(new ProbeData(1, "A", 25.63, 6563, "00"), probes[0]);
        Assert.Equal(new ProbeData(2, "B", 25.74, 6574, "00"), probes[1]);
    }

    [Fact]
    public void Probe_indicator_03_triggers_second_probe_for_other_lengths()
    {
        // 13 bytes (not 12): only the 0x03 indicator says there is a second probe.
        var probes = Parse(0x41, 0x41, 0x5a, 0x03, 0x5a, 0x19, 0xa3, 0x5a, 0x19, 0xae, 0x5a, 0x00, 0x0d);
        Assert.Equal(2, probes.Count);
    }

    [Fact]
    public void Temp_humidity_temp_fifteen_bytes()
    {
        var probes = Parse(
            0x41, 0x41, 0x5a, 0x00,
            0x5a, 0x19, 0xa3, // probe1 temp 25.63
            0x5a, 0x07, 0xd0, // humidity raw 2000
            0x5a, 0x1a, 0x2c, // probe2 temp 27.00
            0x5a, 0x0d);

        Assert.Equal(3, probes.Count);
        Assert.Equal(new ProbeData(1, "A", 25.63, 6563, "00"), probes[0]);
        Assert.Equal(new ProbeData(2, "h", HexProtocolParser.CalcHumidity(2000, 25.63, 0), 2000, "00"), probes[1]);
        Assert.Equal(new ProbeData(3, "A", 27.00, 6700, "00"), probes[2]);
    }

    [Fact]
    public void Temp_humidity_temp_humidity_eighteen_bytes()
    {
        var probes = Parse(
            0x41, 0x41, 0x5a, 0x00,
            0x5a, 0x19, 0xa3,
            0x5a, 0x07, 0xd0,
            0x5a, 0x1a, 0x2c,
            0x5a, 0x07, 0xd0,
            0x5a, 0x0d);

        Assert.Equal([1, 2, 3, 4], probes.Select(p => p.ProbeNo));
        Assert.Equal(["A", "h", "A", "h"], probes.Select(p => p.McuID));
        Assert.Equal(HexProtocolParser.CalcHumidity(2000, 27.00, 0), probes[3].TempValue);
    }

    [Fact]
    public void Broken_sensor_is_parsed_and_left_to_the_caller()
    {
        var p = Assert.Single(Parse(0x41, 0x41, 0x5a, 0x00, 0x5a, 0xFF, 0xFF, 0x5a, 0x0d));
        Assert.Equal(65535, p.RealValue);
    }

    [Fact]
    public void Calc_humidity_matches_sht1x_formula()
    {
        // At 25 °C compensation is zero: -4 + 81 - 11.2 = 65.8
        Assert.Equal(65.8, HexProtocolParser.CalcHumidity(2000, 25.0, 0), 2);
        Assert.Equal(2.5, HexProtocolParser.CalcHumidity(2000, 25.0, 2.5) - HexProtocolParser.CalcHumidity(2000, 25.0, 0), 2);
        // (30-25)*(0.01+0.00008*2000) = 0.85
        Assert.Equal(0.85, HexProtocolParser.CalcHumidity(2000, 30.0, 0) - HexProtocolParser.CalcHumidity(2000, 25.0, 0), 2);
    }

    [Theory]
    [InlineData(25.634, 25.63)]
    [InlineData(25.635, 25.64)]
    [InlineData(0.001, 0.0)]
    [InlineData(-3.555, -3.56)]
    public void Round_to_2_decimals_like_go(double input, double expected)
    {
        Assert.Equal(expected, HexProtocolParser.RoundTo2Decimal(input));
    }

    [Fact]
    public void Filter_drops_out_of_range_temperature_and_humidity()
    {
        var (valid, anomaly) = HexProtocolParser.FilterValidProbes(
        [
            new ProbeData(1, "A", 25.0, 0, "00"),
            new ProbeData(2, "A", 615.35, 0, "00"),
            new ProbeData(3, "h", 101.0, 0, "00"),
            new ProbeData(4, "h", 55.0, 0, "00"),
        ], "test");

        Assert.True(anomaly);
        Assert.Equal([1, 4], valid.Select(p => p.ProbeNo));
    }
}
