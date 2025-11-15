# Bulk Upload Troubleshooting Guide

## Common Issues and Solutions

### High Failure Rate (Many files failed)

**Problem**: "Upload Completed with Errors - X files successful, Y files failed"

**Common Causes & Solutions**:

1. **Invalid PDF Files**
   - **Symptom**: "Not a valid PDF file (missing PDF header)"
   - **Solution**: Ensure all files are actual PDF documents, not images or other formats renamed as .pdf

2. **Empty or Corrupted Files**
   - **Symptom**: "File too small (likely not a valid PDF)" or "Empty file content"
   - **Solution**: Check file integrity, re-download/re-create corrupted PDFs

3. **Text Extraction Failures**
   - **Symptom**: "Both PDF extraction methods failed"
   - **Solution**: PDFs may be image-based scans without OCR text. Use text-based PDFs or convert with OCR

4. **Format Detection Issues**
   - **Symptom**: "Unknown PDF format - content does not match Fremco or Jetting patterns"
   - **Solution**: Check if PDFs contain expected keywords:
     - **Jetting**: "Streckenlänge", "Drehmoment", "Schubkraft"
     - **Fremco**: "Fremco", "SpeedNet", "Blowing distance"

5. **Parsing Failures**
   - **Symptom**: "Jetting/Fremco parsing failed - unable to parse normalized text"
   - **Solution**: PDF structure may be non-standard. Check logs for sample text output

6. **Database Save Errors**
   - **Symptom**: "Database save failed"
   - **Solution**: Check database connection and disk space

## Debugging Steps

### 1. Check Application Logs
```bash
cd /home/azakic/blowing-simulator
docker compose logs app --tail=100 | grep "Bulk upload"
```

### 2. Test Single File First
Before bulk uploading, test a single file through the regular `/pdf2text` interface to ensure it works.

### 3. Verify File Format
Open a few sample PDFs and verify they contain:
- Readable text (not just images)
- Expected format keywords
- Proper measurement tables

### 4. Check File Permissions
Ensure the uploaded files have proper read permissions.

### 5. Monitor Resource Usage
Large bulk uploads may exhaust system resources:
```bash
docker stats blowing-simulator-app-1
```

## Best Practices

### File Selection
- Test a small batch (5-10 files) before processing hundreds
- Group similar files (same equipment type) together
- Avoid mixing different PDF generations or formats

### Upload Configuration
- ✅ **Skip Existing**: Prevents duplicate processing
- ✅ **Continue on Error**: Allows processing of valid files even if some fail
- ✅ **Auto-Save**: Saves time by automatically storing to database

### Performance Tips
- Upload during off-peak hours for better performance
- Split very large batches (>100 files) into smaller chunks
- Monitor disk space - each PDF creates temporary files during processing

## Error Categories

### Validation Errors (Pre-processing)
- Empty file content
- Invalid PDF header
- File too small
- Permission denied

### Extraction Errors (PDF Processing)
- pdftotext command failed
- Go PDF library failed
- No text extracted
- Extracted text too short

### Parsing Errors (Format Processing)
- Format detection failed
- Jetting parsing failed
- Fremco parsing failed
- Missing required fields

### Database Errors (Storage)
- Connection failed
- Duplicate key violation
- Transaction failed
- Disk full

## Recovery Strategies

### Partial Success Scenarios
If some files succeed and others fail:

1. **Review Results**: Check which files succeeded vs failed
2. **Identify Patterns**: Group failures by error type
3. **Fix Issues**: Address specific problems (file format, corruption, etc.)
4. **Re-upload Failures**: Use "Skip Existing" to avoid re-processing successful files

### Complete Failure Scenarios
If all files fail:

1. **Check System Status**: Verify application and database are running
2. **Test Single Upload**: Use `/pdf2text` to test system functionality
3. **Verify File Types**: Ensure files are valid PDFs with text content
4. **Check Logs**: Look for system-level errors in application logs

## Contact Support

If issues persist after troubleshooting:

1. **Collect Information**:
   - Error messages from bulk upload results
   - Application logs (`docker compose logs app`)
   - Sample problematic PDF files
   - System resource usage

2. **Report Issue**:
   - Describe the batch size and file types
   - Include specific error messages
   - Attach relevant log snippets
   - Mention any system configuration changes

## Monitoring

### Health Checks
- Visit `/health` endpoint to verify system status
- Check database connectivity
- Monitor disk space and memory usage

### Log Monitoring
Real-time log monitoring during uploads:
```bash
docker compose logs -f app | grep -E "(Bulk upload|ERROR|WARN)"
```

This will show detailed processing information for each file as it's processed.