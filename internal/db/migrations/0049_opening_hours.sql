CREATE OR REPLACE FUNCTION merchant_is_open(hours JSONB, at_time TIMESTAMPTZ DEFAULT NOW()) RETURNS BOOLEAN
LANGUAGE plpgsql STABLE AS $$
DECLARE local_time TIMESTAMP := at_time AT TIME ZONE 'Asia/Shanghai'; item JSONB; open_t TIME; close_t TIME; weekday INT; previous_day INT; has_special BOOLEAN := FALSE;
BEGIN
  IF hours IS NULL OR jsonb_typeof(hours)<>'array' OR jsonb_array_length(hours)=0 THEN RETURN FALSE; END IF;
  IF EXISTS(SELECT 1 FROM jsonb_array_elements(hours) x WHERE x->>'temporary_closed'='true') THEN RETURN FALSE; END IF;
  weekday := EXTRACT(ISODOW FROM local_time)::INT; previous_day := CASE WHEN weekday=1 THEN 7 ELSE weekday-1 END;
  FOR item IN SELECT value FROM jsonb_array_elements(hours) LOOP
    IF item ? 'date' AND item->>'date'=local_time::date::text THEN
      has_special := TRUE;
      IF item->>'closed'='true' THEN RETURN FALSE; END IF;
      IF item ? 'open' AND item ? 'close' THEN open_t:=(item->>'open')::TIME;close_t:=(item->>'close')::TIME;RETURN (close_t>open_t AND local_time::TIME>=open_t AND local_time::TIME<close_t) OR (close_t<=open_t AND (local_time::TIME>=open_t OR local_time::TIME<close_t));END IF;
    END IF;
  END LOOP;
  IF has_special THEN RETURN FALSE; END IF;
  FOR item IN SELECT value FROM jsonb_array_elements(hours) LOOP
    IF item ? 'day' AND item ? 'open' AND item ? 'close' THEN
      open_t := (item->>'open')::TIME; close_t := (item->>'close')::TIME;
      IF close_t>open_t AND (item->>'day')::INT=weekday AND local_time::TIME>=open_t AND local_time::TIME<close_t THEN RETURN TRUE; END IF;
      IF close_t<=open_t AND (((item->>'day')::INT=weekday AND local_time::TIME>=open_t) OR ((item->>'day')::INT=previous_day AND local_time::TIME<close_t)) THEN RETURN TRUE; END IF;
    END IF;
  END LOOP;
  RETURN FALSE;
EXCEPTION WHEN OTHERS THEN RETURN FALSE;
END $$;
