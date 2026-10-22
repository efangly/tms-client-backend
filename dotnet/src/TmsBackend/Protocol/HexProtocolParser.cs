using System.Text.Json.Serialization;

namespace TmsBackend.Protocol;

/// <summary>One probe reading parsed from a device frame (tcpclient.ProbeData).</summary>
public sealed record ProbeData(
    [property: JsonPropertyName("probeNo")] int ProbeNo,
    [property: JsonPropertyName("mcuId")] string McuID,
    [property: JsonPropertyName("tempValue")] double TempValue,
    [property: JsonPropertyName("realValue")] int RealValue,
    [property: JsonPropertyName("status")] string Status);

/// <summary>
/// Parser for the probe hex protocol (tcpclient.ParseHexResponse), shared by
/// the TCP and serial transports.
/// <code>
/// 1 probe:   41 41 5A 00 5A [t1 t1] 5A 0D                         (9 bytes)
/// 2 probes:  41 41 5A 03 5A [t1 t1] 5A [t2 t2] 5A 0D              (12 bytes)
/// T+H+T:     41 41 5A ?? 5A [t1] 5A [h1] 5A [t2] 5A 0D            (15 bytes)
/// T+H+T+H:   41 41 5A ?? 5A [t1] 5A [h1] 5A [t2] 5A [h2] 5A 0D    (18 bytes)
/// </code>
/// Header 41 42 instead of 41 41 marks an ultra-low (-80) unit with a different formula.
/// </summary>
public static class HexProtocolParser
{
    private const byte Separator = 0x5A;

    // Readings outside these bounds mean a broken/disconnected probe (e.g. -6666 °C).
    private const double MinValidTemp = -100.0;
    private const double MaxValidTemp = 50.0;
    private const double MinValidHumidity = 0.0;
    private const double MaxValidHumidity = 100.0;

    public static List<ProbeData> Parse(ReadOnlySpan<byte> data, string source, ILogger? logger = null)
    {
        var probes = new List<ProbeData>();
        logger?.LogInformation("Received hex data ({Source}): {Hex} ({Length} bytes)", source, FormatHex(data), data.Length);

        if (data.Length < 9)
        {
            logger?.LogWarning("{Source}: buffer too short (expected at least 9 bytes)", source);
            return probes;
        }

        if (data[0] != 0x41 || (data[1] != 0x41 && data[1] != 0x42))
        {
            logger?.LogWarning("{Source}: invalid header, expected 41 41 or 41 42 got {B0:X2} {B1:X2}", source, data[0], data[1]);
            return probes;
        }

        var ultraLow = data[1] == 0x42;
        var probeIndicator = data[3];

        bool hasProbe2 = false, is15Byte = false, is18Byte = false;
        switch (data.Length)
        {
            case 18: is18Byte = true; break;
            case 15: is15Byte = true; break;
            case 12: hasProbe2 = true; break;
            case 9: break;
            default: hasProbe2 = probeIndicator == 0x03; break;
        }

        // Probe 1 — temperature at [5,6]
        double probe1Temp = 0;
        if (data[4] == Separator)
        {
            var raw = Word(data, 5);
            probe1Temp = RawToTemp(raw, ultraLow);
            probes.Add(new ProbeData(1, "A", RoundTo2Decimal(probe1Temp), raw, "00"));
        }
        else
        {
            logger?.LogWarning("{Source}: probe 1 invalid separator at [4], expected 0x5A got 0x{B:X2}", source, data[4]);
        }

        if (is18Byte)
        {
            // [5,6]=temp1 | [8,9]=humidity1 | [11,12]=temp2 | [14,15]=humidity2
            if (data[7] == Separator)
            {
                var raw = Word(data, 8);
                probes.Add(new ProbeData(2, "h", CalcHumidity(raw, probe1Temp, 0), raw, "00"));
            }
            else
            {
                LogBadSeparator(logger, source, "humidity 1", 7, data[7]);
            }

            double probe2Temp = 0;
            if (data[10] == Separator)
            {
                var raw = Word(data, 11);
                probe2Temp = RawToTemp(raw, ultraLow);
                probes.Add(new ProbeData(3, "A", RoundTo2Decimal(probe2Temp), raw, "00"));
            }
            else
            {
                LogBadSeparator(logger, source, "probe 2", 10, data[10]);
            }

            if (data[13] == Separator)
            {
                var raw = Word(data, 14);
                probes.Add(new ProbeData(4, "h", CalcHumidity(raw, probe2Temp, 0), raw, "00"));
            }
            else
            {
                LogBadSeparator(logger, source, "humidity 2", 13, data[13]);
            }
        }
        else if (is15Byte)
        {
            // [5,6]=temp1 | [8,9]=humidity1 | [11,12]=temp2
            if (data[7] == Separator)
            {
                var raw = Word(data, 8);
                probes.Add(new ProbeData(2, "h", CalcHumidity(raw, probe1Temp, 0), raw, "00"));
            }
            else
            {
                LogBadSeparator(logger, source, "humidity 1", 7, data[7]);
            }

            if (data[10] == Separator)
            {
                var raw = Word(data, 11);
                probes.Add(new ProbeData(3, "A", RoundTo2Decimal(RawToTemp(raw, ultraLow)), raw, "00"));
            }
            else
            {
                LogBadSeparator(logger, source, "probe 2", 10, data[10]);
            }
        }
        else if (hasProbe2 && data.Length >= 10)
        {
            // Probe 2 — temperature at [8,9]
            if (data[7] == Separator)
            {
                var raw = Word(data, 8);
                probes.Add(new ProbeData(2, "B", RoundTo2Decimal(RawToTemp(raw, ultraLow)), raw, "00"));
            }
            else
            {
                LogBadSeparator(logger, source, "probe 2", 7, data[7]);
            }
        }

        logger?.LogInformation("{Source}: parsed {Count} probe(s): {Probes}", source, probes.Count,
            string.Join(", ", probes.Select(p => $"#{p.ProbeNo} {p.McuID}={p.TempValue:0.00} (raw {p.RealValue})")));
        return probes;
    }

    /// <summary>
    /// Drops physically implausible readings. McuID "h" is relative humidity
    /// (0–100 %), everything else temperature (-100–50 °C).
    /// </summary>
    public static (List<ProbeData> Valid, bool HasAnomaly) FilterValidProbes(IEnumerable<ProbeData> probes, string source, ILogger? logger = null)
    {
        var valid = new List<ProbeData>();
        var hasAnomaly = false;
        foreach (var p in probes)
        {
            var ok = p.McuID == "h"
                ? p.TempValue is >= MinValidHumidity and <= MaxValidHumidity
                : p.TempValue is >= MinValidTemp and <= MaxValidTemp;
            if (ok)
            {
                valid.Add(p);
                continue;
            }
            logger?.LogWarning("{Source}: anomalous sensor value, skipping probe {ProbeNo} (McuID={McuID} value={Value:0.00})",
                source, p.ProbeNo, p.McuID, p.TempValue);
            hasAnomaly = true;
        }
        return (valid, hasAnomaly);
    }

    /// <summary>Raw probe value to °C: standard (41 41) or ultra-low -80 unit (41 42).</summary>
    public static double RawToTemp(int raw, bool ultraLow) =>
        ultraLow ? -(raw * 0.01) - 48.24 : (raw - 4000) * 0.01;

    /// <summary>
    /// Relative humidity with Sensirion SHT1x temperature compensation:
    /// RH = (-4 + 0.0405 r - 0.0000028 r²) + (T - 25)(0.01 + 0.00008 r) + aVal
    /// </summary>
    public static double CalcHumidity(int rhRaw, double temp, double aVal)
    {
        double r = rhRaw;
        var part1 = -4 + (0.0405 * r) + (-0.0000028 * r * r);
        var part2 = (temp - 25) * (0.01 + (0.00008 * r));
        return RoundTo2Decimal(part1 + part2 + aVal);
    }

    /// <summary>Go math.Round semantics: half away from zero.</summary>
    public static double RoundTo2Decimal(double v) => Math.Round(v * 100, MidpointRounding.AwayFromZero) / 100;

    public static string FormatHex(ReadOnlySpan<byte> data) =>
        string.Join(' ', data.ToArray().Select(b => b.ToString("X2")));

    private static int Word(ReadOnlySpan<byte> data, int index) => (data[index] << 8) | data[index + 1];

    private static void LogBadSeparator(ILogger? logger, string source, string what, int index, byte got) =>
        logger?.LogWarning("{Source}: {What} invalid separator at [{Index}], expected 0x5A got 0x{B:X2}", source, what, index, got);
}
