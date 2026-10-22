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
    public static DateTime Now =>
        DateTime.SpecifyKind(TimeZoneInfo.ConvertTimeFromUtc(DateTime.UtcNow, Zone), DateTimeKind.Unspecified);
}
