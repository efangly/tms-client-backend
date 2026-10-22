using System.Globalization;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace TmsBackend.Json;

/// <summary>
/// JSON settings that reproduce encoding/json output from the Go backend, so
/// the frontend and existing archive files see identical shapes.
/// </summary>
public static class GoJson
{
    public static JsonSerializerOptions Options { get; } = Configure(new JsonSerializerOptions());

    /// <summary>Applies the Go-compatible settings to <paramref name="o"/> (also used for ASP.NET Core's options).</summary>
    public static JsonSerializerOptions Configure(JsonSerializerOptions o)
    {
        // Go writes Thai text as raw UTF-8, not \uXXXX escapes.
        o.Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping;
        o.NumberHandling = JsonNumberHandling.Strict;
        o.Converters.Add(new GoTimeConverter());
        o.Converters.Add(new GoNullableTimeConverter());
        return o;
    }
}

/// <summary>
/// time.Time as Go marshals it: RFC 3339 with the local UTC offset and
/// trailing-zero-trimmed fractional seconds, e.g. "2026-10-22T15:33:53+07:00".
/// DB values are local wall-clock (Go DSN loc=Local), so the offset is the
/// machine's local offset. The zero value is "0001-01-01T00:00:00Z".
/// </summary>
public sealed class GoTimeConverter : JsonConverter<DateTime>
{
    public override DateTime Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options) =>
        Parse(reader.GetString() ?? throw new JsonException("null time"));

    public override void Write(Utf8JsonWriter writer, DateTime value, JsonSerializerOptions options) =>
        writer.WriteStringValue(Format(value));

    public static string Format(DateTime value)
    {
        if (value == DateTime.MinValue)
            return "0001-01-01T00:00:00Z";

        var s = value.ToString("yyyy-MM-dd'T'HH:mm:ss", CultureInfo.InvariantCulture);
        var fraction = value.Ticks % TimeSpan.TicksPerSecond;
        if (fraction != 0)
            s += "." + fraction.ToString("0000000", CultureInfo.InvariantCulture).TrimEnd('0');

        var offset = value.Kind == DateTimeKind.Utc ? TimeSpan.Zero : TimeZoneInfo.Local.GetUtcOffset(value);
        if (offset == TimeSpan.Zero)
            return s + "Z";
        var sign = offset < TimeSpan.Zero ? "-" : "+";
        return s + sign + offset.Duration().ToString(@"hh\:mm", CultureInfo.InvariantCulture);
    }

    /// <summary>Parses an RFC 3339 time into local wall-clock time (Kind=Unspecified), like the DB stores.</summary>
    public static DateTime Parse(string s)
    {
        var dto = DateTimeOffset.Parse(s, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind);
        if (dto.UtcDateTime == DateTime.MinValue)
            return DateTime.MinValue;
        return DateTime.SpecifyKind(TimeZoneInfo.ConvertTime(dto, TimeZoneInfo.Local).DateTime, DateTimeKind.Unspecified);
    }
}

/// <summary>*time.Time: null stays null, otherwise as <see cref="GoTimeConverter"/>.</summary>
public sealed class GoNullableTimeConverter : JsonConverter<DateTime?>
{
    public override DateTime? Read(ref Utf8JsonReader reader, Type typeToConvert, JsonSerializerOptions options) =>
        reader.TokenType == JsonTokenType.Null ? null : GoTimeConverter.Parse(reader.GetString()!);

    public override void Write(Utf8JsonWriter writer, DateTime? value, JsonSerializerOptions options)
    {
        if (value is { } v)
            writer.WriteStringValue(GoTimeConverter.Format(v));
        else
            writer.WriteNullValue();
    }
}
