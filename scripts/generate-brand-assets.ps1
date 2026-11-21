$ErrorActionPreference = "Stop"

$RepoRoot = Split-Path -Parent $PSScriptRoot
$AssetDir = Join-Path $RepoRoot "assets"

$source = @'
using System;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Drawing.Imaging;
using System.IO;

public static class EthifyBrandAssets
{
    private static Color Cyan = Color.FromArgb(78, 227, 214);
    private static Color Green = Color.FromArgb(112, 239, 177);
    private static Color Navy = Color.FromArgb(10, 18, 30);

    public static void Generate(string directory)
    {
        Directory.CreateDirectory(directory);
        using (Bitmap logo = DrawLogo(512))
        {
            logo.Save(Path.Combine(directory, "logo.png"), ImageFormat.Png);
            logo.Save(Path.Combine(directory, "logo_clean.png"), ImageFormat.Png);
            using (Bitmap jpg = new Bitmap(512, 512, PixelFormat.Format24bppRgb))
            using (Graphics graphics = Graphics.FromImage(jpg))
            {
                graphics.Clear(Navy);
                graphics.DrawImage(logo, 0, 0, 512, 512);
                jpg.Save(Path.Combine(directory, "logo.jpg"), ImageFormat.Jpeg);
            }
        }
        DrawBanner(Path.Combine(directory, "banner.jpg"));
        WriteIcon(Path.Combine(directory, "icon.ico"));
    }

    private static Bitmap DrawLogo(int size)
    {
        Bitmap bitmap = new Bitmap(size, size, PixelFormat.Format32bppArgb);
        using (Graphics graphics = Graphics.FromImage(bitmap))
        {
            Prepare(graphics, size);
            graphics.Clear(Color.Transparent);
            DrawMark(graphics, size / 64f);
        }
        return bitmap;
    }

    private static void DrawMark(Graphics graphics, float scale)
    {
        graphics.ScaleTransform(scale, scale);
        using (GraphicsPath tile = RoundedRectangle(3, 3, 58, 58, 16))
        using (LinearGradientBrush tileBrush = new LinearGradientBrush(
            new RectangleF(3, 3, 58, 58), Color.FromArgb(21, 43, 57), Navy, 45f))
        {
            graphics.FillPath(tileBrush, tile);
            using (Pen border = new Pen(Color.FromArgb(65, 103, 116), 1.4f))
                graphics.DrawPath(border, tile);
        }

        using (GraphicsPath glyph = new GraphicsPath())
        {
            glyph.StartFigure();
            glyph.AddLine(47, 14, 25, 14);
            glyph.AddLine(25, 14, 13, 26);
            glyph.AddLine(13, 26, 13, 38);
            glyph.AddLine(13, 38, 25, 50);
            glyph.AddLine(25, 50, 47, 50);
            glyph.StartFigure();
            glyph.AddLine(14, 32, 40, 32);
            using (LinearGradientBrush gradient = new LinearGradientBrush(
                new RectangleF(13, 14, 34, 36), Cyan, Green, 0f))
            using (Pen stroke = new Pen(gradient, 5.2f))
            {
                stroke.StartCap = LineCap.Round;
                stroke.EndCap = LineCap.Round;
                stroke.LineJoin = LineJoin.Round;
                graphics.DrawPath(stroke, glyph);
            }
        }
        using (SolidBrush node = new SolidBrush(Green))
            graphics.FillEllipse(node, 36.2f, 28.2f, 7.6f, 7.6f);
    }

    private static void DrawBanner(string path)
    {
        using (Bitmap bitmap = new Bitmap(1600, 500, PixelFormat.Format24bppRgb))
        using (Graphics graphics = Graphics.FromImage(bitmap))
        {
            graphics.SmoothingMode = SmoothingMode.AntiAlias;
            graphics.Clear(Navy);
            using (LinearGradientBrush background = new LinearGradientBrush(
                new Rectangle(0, 0, 1600, 500), Color.FromArgb(17, 34, 51), Navy, 0f))
                graphics.FillRectangle(background, 0, 0, 1600, 500);

            using (Bitmap mark = DrawLogo(400))
                graphics.DrawImage(mark, 86, 50, 400, 400);

            using (Font title = new Font("Segoe UI", 72, FontStyle.Bold, GraphicsUnit.Pixel))
            using (Font subtitle = new Font("Segoe UI", 24, FontStyle.Regular, GraphicsUnit.Pixel))
            using (LinearGradientBrush wordmark = new LinearGradientBrush(
                new Point(560, 165), new Point(1120, 165), Color.White, Cyan))
            using (SolidBrush subtext = new SolidBrush(Color.FromArgb(172, 190, 204)))
            {
                graphics.DrawString("ETHIFY", title, wordmark, 560, 135);
                graphics.DrawString("WI-FI + ETHERNET. ONE SMARTER CONNECTION.", subtitle, subtext, 568, 235);
            }
            bitmap.Save(path, ImageFormat.Jpeg);
        }
    }

    private static void WriteIcon(string path)
    {
        int[] sizes = new int[] { 16, 24, 32, 48, 64, 128, 256 };
        byte[][] images = new byte[sizes.Length][];
        for (int i = 0; i < sizes.Length; i++)
        {
            using (Bitmap bitmap = new Bitmap(sizes[i], sizes[i], PixelFormat.Format32bppArgb))
            using (MemoryStream stream = new MemoryStream())
            using (Graphics graphics = Graphics.FromImage(bitmap))
            {
                Prepare(graphics, sizes[i]);
                graphics.Clear(Color.Transparent);
                DrawMark(graphics, sizes[i] / 64f);
                bitmap.Save(stream, ImageFormat.Png);
                images[i] = stream.ToArray();
            }
        }

        using (FileStream file = File.Create(path))
        using (BinaryWriter writer = new BinaryWriter(file))
        {
            writer.Write((ushort)0);
            writer.Write((ushort)1);
            writer.Write((ushort)sizes.Length);
            int offset = 6 + 16 * sizes.Length;
            for (int i = 0; i < sizes.Length; i++)
            {
                byte dimension = sizes[i] == 256 ? (byte)0 : (byte)sizes[i];
                writer.Write(dimension);
                writer.Write(dimension);
                writer.Write((byte)0);
                writer.Write((byte)0);
                writer.Write((ushort)1);
                writer.Write((ushort)32);
                writer.Write((uint)images[i].Length);
                writer.Write((uint)offset);
                offset += images[i].Length;
            }
            for (int i = 0; i < images.Length; i++)
                writer.Write(images[i]);
        }
    }

    private static void Prepare(Graphics graphics, int size)
    {
        graphics.SmoothingMode = SmoothingMode.AntiAlias;
        graphics.InterpolationMode = InterpolationMode.HighQualityBicubic;
        graphics.PixelOffsetMode = PixelOffsetMode.HighQuality;
        graphics.CompositingQuality = CompositingQuality.HighQuality;
    }

    private static GraphicsPath RoundedRectangle(float x, float y, float width, float height, float radius)
    {
        GraphicsPath path = new GraphicsPath();
        float diameter = radius * 2;
        path.AddArc(x, y, diameter, diameter, 180, 90);
        path.AddArc(x + width - diameter, y, diameter, diameter, 270, 90);
        path.AddArc(x + width - diameter, y + height - diameter, diameter, diameter, 0, 90);
        path.AddArc(x, y + height - diameter, diameter, diameter, 90, 90);
        path.CloseFigure();
        return path;
    }
}
'@

Add-Type -TypeDefinition $source -ReferencedAssemblies System.Drawing
[EthifyBrandAssets]::Generate($AssetDir)
Write-Host "Generated Ethify logo, banner, and Windows icon in $AssetDir"
