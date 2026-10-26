using System.Globalization;
using System.Text.RegularExpressions;

namespace TmsBackend.Config;

/// <summary>
/// Parses Go-style duration strings ("5m", "5s", "1h30m", "500ms") so the
/// existing .env values (POLL_INTERVAL=5m, ALERT_INTERVAL=5s) keep working.
/// </summary>
public static partial class GoDuration
{
    [GeneratedRegex(@"(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)", RegexOptions.CultureInvariant)]
    private static partial Regex Part();

    public static bool TryParse(string? value, out TimeSpan result)
    {
        result = TimeSpan.Zero;
        if (string.IsNullOrWhiteSpace(value))
            return false;

        var s = value.Trim();
        var matches = Part().Matches(s);
        // Every character must belong to a number+unit pair, e.g. reject "5x" or "5".
        if (matches.Count == 0 || matches.Sum(m => m.Length) != s.Length)
            return false;

        double ticks = 0;
        foreach (Match m in matches)
        {
            var n = double.Parse(m.Groups[1].Value, CultureInfo.InvariantCulture);
            ticks += m.Groups[2].Value switch
            {
                "ns" => n / 100,
                "us" or "µs" => n * 10,
                "ms" => n * TimeSpan.TicksPerMillisecond,
                "s" => n * TimeSpan.TicksPerSecond,
                "m" => n * TimeSpan.TicksPerMinute,
                _ => n * TimeSpan.TicksPerHour,
            };
        }
        result = TimeSpan.FromTicks((long)ticks);
        return true;
    }

    /// <summary>Formats back to the short .env style: "5m", "5s", "1h30m", "1.5s", "250ms".</summary>
    public static string Format(TimeSpan value)
    {
        if (value == TimeSpan.Zero)
            return "0s";
        if (value < TimeSpan.FromSeconds(1))
            return value.TotalMilliseconds.ToString(CultureInfo.InvariantCulture) + "ms";
        var s = "";
        if (value.Hours + value.Days * 24 > 0)
            s += $"{value.Hours + value.Days * 24}h";
        if (value.Minutes > 0)
            s += $"{value.Minutes}m";
        var seconds = value.Seconds + value.Milliseconds / 1000.0;
        if (seconds > 0)
            s += seconds.ToString(CultureInfo.InvariantCulture) + "s";
        return s;
    }
}
