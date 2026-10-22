namespace TmsBackend.Infrastructure;

/// <summary>Thailand (UTC+7) wall-clock time, as database.GetThailandTime in Go.</summary>
public static class ThaiTime
{
    private static readonly TimeZoneInfo Zone = Resolve();

    private static TimeZoneInfo Resolve()
    {
        try
        {
            return TimeZoneInfo.FindSystemTimeZoneById("Asia/Bangkok");
        }
        catch (Exception e) when (e is TimeZoneNotFoundException or InvalidTimeZoneException)
        {
            return TimeZoneInfo.CreateCustomTimeZone("Thailand", TimeSpan.FromHours(7), "Thailand", "Thailand");
        }
    }

    /// <summary>Current Thai time as a DateTime with Kind=Unspecified (what the DB columns store).</summary>
    public static DateTime Now => From(DateTimeOffset.UtcNow);

    /// <summary>Thai wall-clock time for <paramref name="instant"/>, Kind=Unspecified.</summary>
    public static DateTime From(DateTimeOffset instant) =>
        DateTime.SpecifyKind(TimeZoneInfo.ConvertTimeFromUtc(instant.UtcDateTime, Zone), DateTimeKind.Unspecified);

    /// <summary>Thai wall-clock "now" according to <paramref name="time"/> (tests pass a fake clock).</summary>
    public static DateTime ThaiNow(this TimeProvider time) => From(time.GetUtcNow());

    /// <summary>Go time.Truncate(time.Microsecond).</summary>
    public static DateTime TruncateToMicrosecond(this DateTime t) => new(t.Ticks - t.Ticks % 10, t.Kind);
}
